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
	// Strict CSP for user-generated pages:
	// - frame-ancestors 'self': prevent embedding in external sites
	// - default-src 'self': block loading resources from external origins
	// - script-src 'self' 'unsafe-inline': allow inline scripts (pages are Claude-generated HTML)
	//   but block external script loading (prevents XSS via <script src="https://evil.com">)
	// - style-src 'self' 'unsafe-inline': allow inline styles (common in generated pages)
	// - img-src 'self' data:: allow inline data URIs for images
	// - connect-src 'self': restrict fetch/XHR to same origin only
	// - form-action 'self': prevent form submissions to external sites
	// - object-src 'none': block plugins (Flash, Java applets)
	// - base-uri 'self': prevent <base> tag hijacking
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self' 'unsafe-inline'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; "+
			"connect-src 'self'; "+
			"form-action 'self'; "+
			"object-src 'none'; "+
			"base-uri 'self'; "+
			"frame-ancestors 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}
