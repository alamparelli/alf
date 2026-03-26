package controlcenter

import (
	"net/http"
)

// StatusHandler handles GET /api/status.
type StatusHandler struct {
	Provider StatusProvider
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	status := h.Provider.Status()
	respondJSON(w, http.StatusOK, status)
}
