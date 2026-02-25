package controlcenter

import (
	"encoding/json"
	"net/http"
)

// StatusHandler handles GET /api/status.
type StatusHandler struct {
	Provider StatusProvider
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	status := h.Provider.Status()
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	w.Write(data)
}
