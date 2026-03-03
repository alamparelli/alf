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
	w.Write([]byte(h.HTML))
}
