package controlcenter

import (
	"encoding/json"
	"net/http"

	"github.com/alamparelli/alf/internal/firewall"
)

// FirewallHandler serves GET/PUT for firewall config and request log.
type FirewallHandler struct {
	Store       *firewall.Store
	Proxy       *firewall.Proxy
	Notifier    Notifier
	EventBroker *EventBroker
}

func (h *FirewallHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Proxy == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "firewall not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, err := h.Store.Load()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"config": cfg,
			"log":    h.Proxy.Log.Entries(),
		})

	case http.MethodPut:
		var cfg firewall.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if cfg.Mode != firewall.ModeLogOnly && cfg.Mode != firewall.ModeEnforce {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be 'log-only' or 'enforce'"})
			return
		}
		if err := h.Store.Save(&cfg); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		if h.Notifier != nil {
			h.Notifier.Notify(ReloadFirewall)
		}
		if h.EventBroker != nil {
			h.EventBroker.Emit(EventFirewall)
		}
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})

	case http.MethodDelete:
		h.Proxy.Log.Clear()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		methodNotAllowed(w)
	}
}
