package controlcenter

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

// allowedToolsPaths are the ONLY CC endpoints accessible via the tools socket.
// Everything else is blocked. The tools socket is reachable by the LLM subprocess
// — only safe, read-oriented or tool-specific endpoints are permitted.
var allowedToolsPaths = map[string]bool{
	"/api/tasks":             true, // task list/launch/get/cancel
	"/api/teams":             true, // team list/get
	"/api/skills/catalog":    true, // skill list
	"/api/tiers":             true, // tier list (read-only)
	"/api/config":            true, // config read (GET only, PUT blocked below)
	"/api/logs":              true, // log list/tail
	"/api/search":            true, // search
	"/api/llm/invoke":        true, // LLM invocation (used by system-tools)
	"/health":                true, // health check
}

// allowedToolsPrefixes are path prefixes allowed on the tools socket.
var allowedToolsPrefixes = []string{
	"/api/apps/",        // app list/details
	"/api/marketplace/", // marketplace catalog/install
}

// blockedToolsMethods prevents write operations on read-only endpoints.
var blockedToolsMethods = map[string][]string{
	"/api/config": {"PUT", "POST", "DELETE"}, // config is read-only for LLM
}

// ToolsProxy serves a filtered subset of the CC HTTP API over a Unix socket.
// System-tools connect here instead of using CC_AUTH_TOKEN + localhost:8080.
// Socket access (mode 0660, group alf) IS the authentication.
// Uses ALLOWLIST — only explicitly permitted endpoints are accessible.
type ToolsProxy struct {
	Handler http.Handler // the main CC mux
}

func (tp *ToolsProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Canonicalize path: strip trailing slashes to prevent bypass.
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	// Check allowlist.
	allowed := allowedToolsPaths[path]
	if !allowed {
		for _, prefix := range allowedToolsPrefixes {
			if strings.HasPrefix(path+"/", prefix) {
				allowed = true
				break
			}
		}
	}

	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Check method restrictions.
	if methods, ok := blockedToolsMethods[path]; ok {
		for _, m := range methods {
			if r.Method == m {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
	}

	// Inject internal auth marker so the auth middleware passes.
	r.Header.Set("X-Tools-Socket", "1")

	tp.Handler.ServeHTTP(w, r)
}

// ListenAndServeTools starts the tools proxy on a Unix socket.
// The caller should defer closing the listener and removing the socket file.
func ListenAndServeTools(sockPath string, handler http.Handler) (net.Listener, error) {
	os.Remove(sockPath) // remove stale socket

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}

	// Daemon runs as alfd (uid 1001, gid 1001). Set group to alf (1000) for subprocess access.
	os.Chown(sockPath, -1, 1000)
	os.Chmod(sockPath, 0660)

	proxy := &ToolsProxy{Handler: handler}
	go func() {
		srv := &http.Server{Handler: proxy}
		if err := srv.Serve(ln); err != nil && !strings.Contains(err.Error(), "use of closed") {
			log.Printf("[tools-proxy] serve error: %v", err)
		}
	}()

	log.Printf("[tools-proxy] listening on %s", sockPath)
	return ln, nil
}
