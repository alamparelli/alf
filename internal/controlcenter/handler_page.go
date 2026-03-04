package controlcenter

import (
	"net/http"
	"strings"
)

// PageHandler serves raw HTML pages from a ResourceStore.
// GET /pages/{name} → raw text/html response.
type PageHandler struct {
	Store ResourceStore
}

func (h *PageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract page name: /pages/foo → foo
	name := strings.TrimPrefix(r.URL.Path, "/pages/")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	if !validName.MatchString(name) {
		http.NotFound(w, r)
		return
	}

	data, err := h.Store.Get(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write(data)
}
