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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
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
				"script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https:; "+
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	apps, err := h.Store.List()
	if err != nil {
		http.Error(w, `{"error":"failed to list apps"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"items":`))
	if len(apps) == 0 {
		w.Write([]byte(`[]`))
	} else {
		first := true
		w.Write([]byte(`[`))
		for _, a := range apps {
			if !first {
				w.Write([]byte(`,`))
			}
			first = false
			// Manual JSON to avoid import cycle or extra dependency.
			w.Write([]byte(`{"name":` + jsonStr(a.Name)))
			if a.DisplayName != "" {
				w.Write([]byte(`,"display_name":` + jsonStr(a.DisplayName)))
			}
			if a.Icon != "" {
				w.Write([]byte(`,"icon":` + jsonStr(a.Icon)))
			}
			if a.Description != "" {
				w.Write([]byte(`,"description":` + jsonStr(a.Description)))
			}
			w.Write([]byte(`,"mod_time":` + jsonStr(a.ModTime) + `}`))
		}
		w.Write([]byte(`]`))
	}
	w.Write([]byte(`}`))
}

// jsonStr returns a JSON-encoded string value.
func jsonStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
