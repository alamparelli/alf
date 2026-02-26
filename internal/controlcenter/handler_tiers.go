package controlcenter

import (
	"encoding/json"
	"net/http"
)

// TiersHandler handles GET /api/tiers (read-only).
type TiersHandler struct {
	Store TierStore
}

func (h *TiersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *TiersHandler) get(w http.ResponseWriter, _ *http.Request) {
	tiers, err := h.Store.Load()
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	data, err := json.MarshalIndent(tiers, "", "  ")
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	w.Write(data)
}
