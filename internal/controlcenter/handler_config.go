package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ConfigHandler handles GET and PUT /api/config.
type ConfigHandler struct {
	Store    ConfigStore
	Notifier Notifier
	Event    ReloadEvent
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
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}

	if cfg.LogLevel != "" {
		allowed := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
		if !allowed[cfg.LogLevel] {
			http.Error(w, jsonErr(fmt.Sprintf("invalid log_level %q", cfg.LogLevel)), http.StatusBadRequest)
			return
		}
	}

	if cfg.QuietHours.Start < 0 || cfg.QuietHours.Start > 23 || cfg.QuietHours.End < 0 || cfg.QuietHours.End > 23 {
		http.Error(w, jsonErr("quiet_hours start/end must be 0-23"), http.StatusBadRequest)
		return
	}

	if cfg.SessionTimeout < 0 {
		http.Error(w, jsonErr("session_timeout must be >= 0"), http.StatusBadRequest)
		return
	}

	if cfg.GitSweepInterval < 0 {
		http.Error(w, jsonErr("git_sweep_interval must be >= 0"), http.StatusBadRequest)
		return
	}

	if cfg.AutoUpdateCheckInterval < 0 {
		http.Error(w, jsonErr("auto_update_check_interval must be >= 0"), http.StatusBadRequest)
		return
	}

	if err := h.Store.Save(&cfg); err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	if h.Notifier != nil {
		h.Notifier.Notify(h.Event)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func jsonErr(msg string) string {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return string(data)
}
