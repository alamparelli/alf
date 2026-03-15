package controlcenter

import "net/http"

// HealthHandler handles GET /health.
type HealthHandler struct{}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
