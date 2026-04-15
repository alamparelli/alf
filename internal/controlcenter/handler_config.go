package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ConfigHandler handles GET and PUT /api/config.
type ConfigHandler struct {
	Store       ConfigStore
	Notifier    Notifier
	Event       ReloadEvent
	EventBroker *EventBroker
}

func (h *ConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r)
	case http.MethodPut:
		h.put(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *ConfigHandler) get(w http.ResponseWriter, _ *http.Request) {
	cfg, err := h.Store.Load()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	redacted, err := RedactJSON(data)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(redacted)
}

func (h *ConfigHandler) put(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if cfg.LogLevel != "" {
		allowed := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
		if !allowed[cfg.LogLevel] {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid log_level %q", cfg.LogLevel))
			return
		}
	}

	if cfg.QuietHours.Start < 0 || cfg.QuietHours.Start > 23 || cfg.QuietHours.End < 0 || cfg.QuietHours.End > 23 {
		respondError(w, http.StatusBadRequest, "quiet_hours start/end must be 0-23")
		return
	}

	if cfg.SessionTimeout < 0 {
		respondError(w, http.StatusBadRequest, "session_timeout must be >= 0")
		return
	}

	if cfg.GitSweepInterval < 0 {
		respondError(w, http.StatusBadRequest, "git_sweep_interval must be >= 0")
		return
	}

	if cfg.AutoUpdateCheckInterval < 0 {
		respondError(w, http.StatusBadRequest, "auto_update_check_interval must be >= 0")
		return
	}

	for name, b := range cfg.Backends {
		if b.BaseURL == "" {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("backend %q: base_url is required", name))
			return
		}
		if b.Auth != "" && b.Auth != "bearer" && b.Auth != "none" {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("backend %q: invalid auth %q (must be \"bearer\" or \"none\")", name, b.Auth))
			return
		}
	}

	// Preserve backends from existing config when not provided in the request
	// (frontend strips redacted backends to avoid sending masked secrets).
	if cfg.Backends == nil {
		existing, err := h.Store.Load()
		if err == nil && existing.Backends != nil {
			cfg.Backends = existing.Backends
		}
	}

	if err := h.Store.Save(&cfg); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	notifyReload(h.Notifier, h.Event)
	h.EventBroker.Emit(EventConfig)

	respondOK(w)
}

