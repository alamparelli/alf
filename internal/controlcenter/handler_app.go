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

// AppHandler serves files from app directories and proxies API requests
// to REST server apps that declare a port in data/port.
//
// Static files:
//
//	GET /apps/{name}           → serves index.html
//	GET /apps/{name}/          → serves index.html
//	GET /apps/{name}/file.css  → serves the file with correct MIME type
//
// API proxy (any method):
//
//	/apps/{name}/api/...       → reverse proxy to localhost:{port}/api/...
//	Port is read from apps/{name}/data/port (written by the app server at startup).
type AppHandler struct {
	Store AppStore
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

	data, err := h.Store.ReadFile(appName, filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Determine content type from extension.
	ext := filepath.Ext(filePath)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)

	// Security headers for HTML files.
	if ext == ".html" || ext == ".htm" {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://unpkg.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob: https:; "+
				"connect-src 'self'; "+
				"form-action 'self'; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"frame-ancestors 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
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
			// Don't forward auth cookies to app servers.
			req.Header.Del("Cookie")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, `{"error":"app server unreachable"}`, http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
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
