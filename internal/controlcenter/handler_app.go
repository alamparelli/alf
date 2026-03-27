package controlcenter

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// AppHandler serves files from app directories.
// GET /apps/{name}           → serves index.html
// GET /apps/{name}/          → serves index.html
// GET /apps/{name}/file.css  → serves the file with correct MIME type
type AppHandler struct {
	Store AppStore
}

func (h *AppHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Extract: /apps/my-app/assets/style.css → "my-app/assets/style.css"
	rest := strings.TrimPrefix(r.URL.Path, "/apps/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}

	// Split into app name and file path.
	var appName, filePath string
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		appName = rest[:idx]
		filePath = rest[idx+1:]
	} else {
		appName = rest
		filePath = ""
	}

	if !validName.MatchString(appName) {
		http.NotFound(w, r)
		return
	}

	// Default to index.html.
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
