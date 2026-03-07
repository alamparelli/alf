package controlcenter

import (
	"encoding/json"
	"net/http"
)

// TiersHandler serves the full tiers configuration for the CC tiers tab.
type TiersHandler struct {
	TierStore TierStore
	Notifier  Notifier
}

func (h *TiersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := h.TierStore.Current()
		respondJSON(w, http.StatusOK, cfg)

	case http.MethodPut:
		var cfg TiersConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := validateTiersConfig(&cfg); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := h.TierStore.Save(&cfg); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		if h.Notifier != nil {
			h.Notifier.Notify(ReloadTiers)
		}
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func validateTiersConfig(cfg *TiersConfig) error {
	names := map[string]bool{}
	for _, t := range cfg.Tiers {
		if t.Name == "" {
			return errVal("tier name is required")
		}
		if names[t.Name] {
			return errVal("duplicate tier name: " + t.Name)
		}
		names[t.Name] = true
		// Skip model validation for openrouter tiers (any model ID is valid).
		if t.Backend != "openrouter" && !AllowedModels[t.Model] {
			return errVal("invalid model for tier " + t.Name + ": " + t.Model)
		}
		if t.Effort != "" && !AllowedEfforts[t.Effort] {
			return errVal("invalid effort for tier " + t.Name + ": " + t.Effort)
		}
		if !AllowedBackends[t.Backend] {
			return errVal("invalid backend for tier " + t.Name + ": " + t.Backend)
		}
	}
	if cfg.RouterModel != "" && cfg.RouterBackend != "openrouter" && !AllowedModels[cfg.RouterModel] {
		return errVal("invalid router_model: " + cfg.RouterModel)
	}
	if !AllowedBackends[cfg.RouterBackend] {
		return errVal("invalid router_backend: " + cfg.RouterBackend)
	}
	return nil
}

type valError struct{ msg string }

func (e *valError) Error() string { return e.msg }
func errVal(msg string) error    { return &valError{msg: msg} }
