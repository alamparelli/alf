package controlcenter

import (
	"net/http"
	"strings"
)

// DashboardHandler serves the embedded HTML dashboard.
type DashboardHandler struct {
	HTML  string // Raw HTML content from embedded file
	Token string // Auth token to inject
}

func (h *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	html := strings.Replace(h.HTML, "{{AUTH_TOKEN}}", h.Token, 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}
