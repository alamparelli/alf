package wasm

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// osStat is a tiny shim so callers don't need to import os directly.
var osStat = os.Stat

// AppRouter serves discovered WASM apps under /wasm-app/<name>/... and
// any static frontend directory they ship under /wasm-app/<name>/static/...
//
// Each app request instantiates the guest as a WASI command (CGI-style)
// with ALF_METHOD / ALF_PATH / ALF_BODY_LENGTH env + body on stdin.
//
// Thread-safe: Register/Unregister/ServeHTTP can run concurrently.
type AppRouter struct {
	runtime *Runtime

	mu   sync.RWMutex
	apps map[string]*registeredApp // key: manifest.Name
}

type registeredApp struct {
	manifestPath string
	manifest     *Manifest
	frontendDir  string // absolute path; empty if none
	staticFS     http.Handler
}

// NewAppRouter returns an empty router. Callers invoke Register for each
// discovered app.
func NewAppRouter(rt *Runtime) *AppRouter {
	return &AppRouter{runtime: rt, apps: map[string]*registeredApp{}}
}

// Register adds an app. If a frontend/ directory exists next to the
// manifest, it is served under /wasm-app/<name>/static/. The app's own
// HTTP handler is invoked for every other sub-path.
func (r *AppRouter) Register(d DiscoveredCapability) {
	if d.Manifest.Kind != KindApp {
		log.Printf("[wasm-app] skip %s: kind=%s (expected app)", d.Manifest.Name, d.Manifest.Kind)
		return
	}
	manDir := filepath.Dir(d.ManifestPath)
	app := &registeredApp{
		manifestPath: d.ManifestPath,
		manifest:     d.Manifest,
	}
	frontend := filepath.Join(manDir, "frontend")
	if st, err := osStat(frontend); err == nil && st.IsDir() {
		app.frontendDir = frontend
		app.staticFS = http.FileServer(http.Dir(frontend))
	}
	r.mu.Lock()
	r.apps[d.Manifest.Name] = app
	r.mu.Unlock()
	log.Printf("[wasm-app] registered %q (frontend=%v)", d.Manifest.Name, app.frontendDir != "")
}

// Unregister drops an app. No-op if absent.
func (r *AppRouter) Unregister(name string) {
	r.mu.Lock()
	delete(r.apps, name)
	r.mu.Unlock()
}

// Names returns the registered app names, for listing/debug.
func (r *AppRouter) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.apps))
	for n := range r.apps {
		out = append(out, n)
	}
	return out
}

// ServeHTTP implements http.Handler. Paths routed:
//
//	/wasm-app/                → index page listing all apps
//	/wasm-app/<name>/         → app's "/" handler (HTML by default)
//	/wasm-app/<name>/static/… → frontend/<path> if frontend exists
//	/wasm-app/<name>/<rest>   → app handler with ALF_PATH = "/<rest>"
//
// 404 is returned only when the app is unknown. Unknown sub-paths go
// to the app — it decides.
func (r *AppRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/wasm-app/")
	if rest == req.URL.Path {
		http.NotFound(w, req)
		return
	}
	if rest == "" || rest == "/" {
		r.writeIndex(w)
		return
	}
	slash := strings.IndexByte(rest, '/')
	var name, subPath string
	if slash < 0 {
		name = rest
		subPath = "/"
	} else {
		name = rest[:slash]
		subPath = rest[slash:]
	}

	r.mu.RLock()
	app, ok := r.apps[name]
	r.mu.RUnlock()
	if !ok {
		http.Error(w, "wasm app not found: "+name, http.StatusNotFound)
		return
	}

	// Static frontend passthrough.
	if app.staticFS != nil && strings.HasPrefix(subPath, "/static/") {
		http.StripPrefix("/wasm-app/"+name+"/static/", app.staticFS).ServeHTTP(w, req)
		return
	}
	if app.staticFS != nil && subPath == "/" {
		// Serve index.html from the frontend dir if present.
		indexPath := filepath.Join(app.frontendDir, "index.html")
		if _, err := osStat(indexPath); err == nil {
			http.ServeFile(w, req, indexPath)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	t0 := time.Now()
	status, respBody, err := r.runtime.InvokeApp(req.Context(), app.manifestPath, req.Method, subPath, body)
	elapsed := time.Since(t0)
	log.Printf("[wasm-app] %s %s%s -> %d (%.1fms)", req.Method, "/wasm-app/"+name, subPath, status, float64(elapsed.Microseconds())/1000)

	if err != nil {
		http.Error(w, "guest error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(respBody) > 0 {
		switch {
		case respBody[0] == '{' || respBody[0] == '[':
			w.Header().Set("Content-Type", "application/json")
		case strings.HasPrefix(string(respBody), "<"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	w.WriteHeader(status)
	w.Write(respBody)
}

// writeIndex emits a tiny HTML landing page listing registered apps.
// Useful at /wasm-app/ for a sanity check after deployment.
func (r *AppRouter) writeIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>WASM apps</title>
<style>body{font-family:system-ui;max-width:640px;margin:40px auto;padding:0 20px}code{background:#f4f4f4;padding:2px 6px;border-radius:4px}</style>
<h1>WASM apps</h1>`)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.apps) == 0 {
		io.WriteString(w, `<p>No WASM apps registered. Place a manifest + .wasm under <code>/home/alf/data/wasm-apps/&lt;name&gt;/</code> and restart the daemon.</p>`)
		return
	}
	io.WriteString(w, `<ul>`)
	for name, a := range r.apps {
		io.WriteString(w, `<li><a href="/wasm-app/`+name+`/">`+name+`</a> — `+a.manifest.Description+`</li>`)
	}
	io.WriteString(w, `</ul>`)
}
