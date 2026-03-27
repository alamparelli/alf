package controlcenter

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

// blockedToolsPaths are CC endpoints that must NOT be accessible via the tools socket.
// The tools socket is reachable by the LLM subprocess — these endpoints would
// allow privilege escalation or admin-level access.
var blockedToolsPaths = map[string]bool{
	"/api/bash":       true, // shell execution as daemon user
	"/api/restart":    true, // restart the daemon
	"/api/magic-link": true, // generate login links
	"/api/terminal":   true, // terminal WebSocket
}

// blockedToolsPrefixes are path prefixes blocked on the tools socket.
var blockedToolsPrefixes = []string{
	"/api/vault/", // vault management (unlock, lock, reset, secrets)
}

// ToolsProxy serves a filtered subset of the CC HTTP API over a Unix socket.
// System-tools connect here instead of using CC_AUTH_TOKEN + localhost:8080.
// Socket access (mode 0660, group alf) IS the authentication.
type ToolsProxy struct {
	Handler http.Handler // the main CC mux
}

func (tp *ToolsProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if blockedToolsPaths[path] {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	for _, prefix := range blockedToolsPrefixes {
		if strings.HasPrefix(path, prefix) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
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

	// Mode 0660: daemon (owner) + alf group (subprocess) can connect.
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
