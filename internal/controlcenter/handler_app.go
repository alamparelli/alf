package controlcenter

import (
	"fmt"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// allowedStaticExt defines file extensions that can be served via the static
// file handler. All other extensions are blocked to prevent leaking source
// code, databases, configs, and other sensitive files. Edit this map to allow
// additional file types.
var allowedStaticExt = map[string]bool{
	// Web
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true,
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".ico": true, ".webp": true, ".avif": true,
	// Fonts
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	// Media
	".mp3": true, ".ogg": true, ".wav": true, ".mp4": true, ".webm": true,
	// Data (read-only, explicitly allowed)
	".json": true, ".xml": true, ".txt": true, ".csv": true,
	// WebAssembly / game engines
	".wasm": true, ".pck": true,
	// Maps
	".map": true,
}

// AppHandler serves files from app directories and proxies API requests
// to REST server apps that declare a port in data/port.
//
// Static files:
//
//	GET /apps/{name}           → serves index.html
//	GET /apps/{name}/          → serves index.html
//	GET /apps/{name}/file.css  → serves the file with correct MIME type
//
// Only files with extensions in allowedStaticExt are served.
// Source code (.go, .py, .rs), databases (.db, .sqlite), and internal
// files (data/port) are blocked with 404.
//
// API proxy (any method):
//
//	/apps/{name}/api/...       → reverse proxy to localhost:{port}/api/...
//	Port is read from apps/{name}/data/port (written by the app server at startup).
type AppHandler struct {
	Store     AppStore
	AppTokens *AppTokenStore // validates Bearer tokens from sandboxed iframes
}

func (h *AppHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract: /apps/my-app/api/items → "my-app/api/items"
	rest := strings.TrimPrefix(r.URL.Path, "/apps/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}

	// Split into app name and sub-path.
	var appName, subPath string
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		appName = rest[:idx]
		subPath = rest[idx+1:]
	} else {
		appName = rest
		subPath = ""
	}

	if !validName.MatchString(appName) {
		http.NotFound(w, r)
		return
	}

	// API proxy: /apps/{slug}/api/... → localhost:{port}/api/...
	if strings.HasPrefix(subPath, "api/") || subPath == "api" {
		// SEC-005: Block cross-app API access. Only the app's own iframe
		// (or non-app callers) may use its API proxy.
		callerApp := extractAppSlugFromReferer(r)
		if callerApp != "" && callerApp != appName {
			http.Error(w, `{"error":"cross-app API access denied"}`, http.StatusForbidden)
			return
		}
		h.proxyAPI(w, r, appName, "/"+subPath)
		return
	}

	// Static file serving (GET only).
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Default to index.html.
	filePath := subPath
	if filePath == "" || filePath == "/" {
		filePath = "index.html"
	}

	// SEC-006: Only serve files with allowed web extensions.
	// Blocks source code (.go, .py), databases (.db, .sqlite),
	// internal files (data/port), and anything not explicitly allowed.
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" || !allowedStaticExt[ext] {
		http.NotFound(w, r)
		return
	}

	data, err := h.Store.ReadFile(appName, filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Determine content type from extension.
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)

	// Security headers for HTML files.
	if ext == ".html" || ext == ".htm" {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval' https://unpkg.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob:; "+
				"connect-src 'self'; "+
				"form-action 'self'; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"frame-ancestors 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Inject alf-ui.css into <head> so the browser loads it with the
		// page — eliminates FOUC from post-load CSS injection.
		data = injectDesignSystemCSS(data)
	}

	w.Write(data)
}

// proxyAPI reverse-proxies to the app's local REST server.
// The port is read from apps/{slug}/data/port.
func (h *AppHandler) proxyAPI(w http.ResponseWriter, r *http.Request, slug, apiPath string) {
	// Read port from data/port file.
	portData, err := h.Store.ReadFile(slug, "data/port")
	if err != nil {
		http.Error(w, `{"error":"app has no running server"}`, http.StatusBadGateway)
		return
	}

	port, err := strconv.Atoi(strings.TrimSpace(string(portData)))
	if err != nil || port < 1024 || port > 65535 {
		http.Error(w, `{"error":"invalid app port"}`, http.StatusBadGateway)
		return
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = apiPath
			req.URL.RawQuery = r.URL.RawQuery
			req.Host = target.Host
			// SEC-004: Strip sensitive headers — app servers are untrusted.
			req.Header.Del("Cookie")
			req.Header.Del("Authorization")
			req.Header.Del("X-Tools-Socket")
			req.Header.Del("X-Requested-With")
			// SEC-007: Strip forwarded headers to prevent host header injection.
			req.Header.Del("X-Forwarded-Host")
			req.Header.Del("X-Forwarded-For")
			req.Header.Del("X-Forwarded-Proto")
			req.Header.Del("X-Real-Ip")
		},
		ModifyResponse: func(resp *http.Response) error {
			// Strip CORS headers set by backend apps — the CC CORS middleware
			// is authoritative. Duplicate headers cause browser rejections.
			resp.Header.Del("Access-Control-Allow-Origin")
			resp.Header.Del("Access-Control-Allow-Methods")
			resp.Header.Del("Access-Control-Allow-Headers")
			resp.Header.Del("Access-Control-Allow-Credentials")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, `{"error":"app server unreachable"}`, http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

// injectDesignSystemCSS inserts a <link> to alf-ui.css into the HTML <head>
// so the browser loads it in parallel with the page, preventing FOUC.
func injectDesignSystemCSS(html []byte) []byte {
	const tag = `<link rel="stylesheet" href="/static/alf-ui.css">`
	s := string(html)

	// Already has it — don't double-inject.
	// Check for the exact tag we inject. This avoids false positives from
	// HTML-escaped mentions in <code> blocks (e.g., &lt;link ... alf-ui.css).
	if strings.Contains(s, tag) {
		return html
	}

	// Inject right after <head> (or <head ...>).
	if idx := strings.Index(strings.ToLower(s), "</head>"); idx >= 0 {
		return []byte(s[:idx] + tag + s[idx:])
	}
	// Fallback: prepend to the document.
	return []byte(tag + s)
}

// AppListHandler returns JSON list of installed apps.
// GET /api/apps/ → {"items": [...]}
type AppListHandler struct {
	Store AppStore
}

func (h *AppListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	apps, err := h.Store.List()
	if err != nil {
		http.Error(w, `{"error":"failed to list apps"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"items": apps})
}
