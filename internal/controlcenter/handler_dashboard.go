package controlcenter

import (
	"net/http"
)

// DashboardHandler serves the embedded HTML dashboard.
type DashboardHandler struct {
	HTML string // Raw HTML content from embedded file
}

func (h *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self' https://unpkg.com; "+
			"style-src 'self' 'unsafe-inline' https://unpkg.com; "+
			"connect-src 'self' wss: https://unpkg.com; "+
			"img-src 'self' data:; "+
			"frame-ancestors 'self'")
	w.Write([]byte(h.HTML))
}
