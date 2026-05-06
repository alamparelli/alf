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
	"/api/tasks/chain":       true, // task chain (fire-and-forget LLM pipeline)
	"/api/teams":             true, // team list/get
	"/api/skills/catalog":    true, // skill list
	"/api/tiers":             true, // tier list (read-only)
	"/api/config":            true, // config read (GET only, PUT blocked below)
	"/api/logs":              true, // log list/tail
	"/api/search":            true, // search
	"/api/llm/invoke":        true, // LLM invocation (used by system-tools)
	"/api/settings/avatar":   true, // avatar management
	"/api/wasm/build":        true, // #386 step 8 — wasm_build_tool dispatch (cli/codex parallel of WASMBuildNativeTool)
	"/health":                true, // health check
}

// allowedToolsPrefixes are path prefixes allowed on the tools socket.
var allowedToolsPrefixes = []string{
	"/api/apps/",        // app list/details
	"/api/marketplace/", // marketplace catalog/install
	"/api/developer/",   // developer tools (publish, validate)
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

// AppToolsProxy is a per-app variant of ToolsProxy served over a socket mounted
// into one specific app's sandbox. Requests are tagged with the app's slug so
// downstream handlers can enforce per-app permissions (bash, network, etc.).
//
// Allowlist is a superset of ToolsProxy: apps get the same read/tool endpoints
// PLUS /api/bash (permission-gated) and their own /api/apps/{slug}/... scope.
// Install/marketplace endpoints are blocked — apps must not install other apps.
type AppToolsProxy struct {
	Slug    string
	Handler http.Handler // the main CC mux
}

// appAllowedPaths = allowedToolsPaths minus nothing, plus /api/bash.
var appAllowedPaths = func() map[string]bool {
	m := make(map[string]bool, len(allowedToolsPaths)+1)
	for k, v := range allowedToolsPaths {
		m[k] = v
	}
	m["/api/bash"] = true
	return m
}()

// appAllowedPrefixes = allowedToolsPrefixes minus marketplace/developer.
// Apps can reach /api/apps/ (scoped to their own slug by downstream handlers).
var appAllowedPrefixes = []string{
	"/api/apps/",
}

func (ap *AppToolsProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	allowed := appAllowedPaths[path]
	if !allowed {
		for _, prefix := range appAllowedPrefixes {
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

	if methods, ok := blockedToolsMethods[path]; ok {
		for _, m := range methods {
			if r.Method == m {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
	}

	// Socket presence is authentication; the slug is bound to the socket path.
	r.Header.Set("X-Tools-Socket", "1")
	r.Header.Set("X-Tools-Socket-App", ap.Slug)

	ap.Handler.ServeHTTP(w, r)
}

// ListenAndServeAppTools starts a per-app tools proxy on a Unix socket.
// The socket is chmod 0660 and owned by group alf so the app's uid 1000 can reach it.
func ListenAndServeAppTools(sockPath, slug string, handler http.Handler) (net.Listener, error) {
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	os.Chown(sockPath, -1, 1000)
	os.Chmod(sockPath, 0660)

	proxy := &AppToolsProxy{Slug: slug, Handler: handler}
	go func() {
		srv := &http.Server{Handler: proxy}
		if err := srv.Serve(ln); err != nil && !strings.Contains(err.Error(), "use of closed") {
			log.Printf("[app-tools-proxy:%s] serve error: %v", slug, err)
		}
	}()
	log.Printf("[app-tools-proxy:%s] listening on %s", slug, sockPath)
	return ln, nil
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
