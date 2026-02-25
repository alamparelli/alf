package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
)

// ConfigHandler handles GET/PUT /api/config.
type ConfigHandler struct {
	Store    ConfigStore
	Notifier Notifier
}

func (h *ConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r)
	case http.MethodPut:
		h.put(w, r)
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

func (h *ConfigHandler) put(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, jsonErr("failed to read body"), http.StatusBadRequest)
		return
	}

	if err := ValidateConfigJSON(body); err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		return
	}

	current, err := h.Store.Load()
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}
	currentData, _ := json.Marshal(current)

	restored, err := RestoreRedacted(body, currentData)
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		return
	}

	var cfg Config
	if err := json.Unmarshal(restored, &cfg); err != nil {
		http.Error(w, jsonErr("invalid config: "+err.Error()), http.StatusBadRequest)
		return
	}

	if err := ValidateConfig(&cfg); err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		return
	}

	if err := h.Store.Save(&cfg); err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	if h.Notifier != nil {
		h.Notifier.Notify(ReloadConfig)
	}

	w.Write([]byte(`{"ok":true}`))
}

func jsonErr(msg string) string {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return string(data)
}
