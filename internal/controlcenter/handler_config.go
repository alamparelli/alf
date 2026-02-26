package controlcenter

import (
	"encoding/json"
	"net/http"
)

// ConfigHandler handles GET /api/config (read-only).
type ConfigHandler struct {
	Store ConfigStore
}

func (h *ConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *ConfigHandler) get(w http.ResponseWriter, _ *http.Request) {
	cfg, err := h.Store.Load()
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	redacted, err := RedactJSON(data)
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	w.Write(redacted)
}

func jsonErr(msg string) string {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return string(data)
}
