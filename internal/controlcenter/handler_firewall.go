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
	NetTracker  *firewall.NetTracker
	Notifier    Notifier
	EventBroker *EventBroker
}

func (h *FirewallHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Proxy == nil {
		respondError(w, http.StatusServiceUnavailable, "firewall not available")
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, err := h.Store.Load()
		if err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp := map[string]any{
			"config": cfg,
			"log":    h.Proxy.Log.Entries(),
		}
		if h.Store != nil {
			resp["hosts"] = h.Store.Hosts()
		}
		if h.NetTracker != nil {
			resp["kill_switch"] = h.NetTracker.KillSwitchActive()
		}
		respondJSON(w, http.StatusOK, resp)

	case http.MethodPut:
		var cfg firewall.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON: " + err.Error())
			return
		}
		if cfg.Mode != firewall.ModeLogOnly && cfg.Mode != firewall.ModeEnforce {
			respondError(w, http.StatusBadRequest, "mode must be 'log-only' or 'enforce'")
			return
		}
		if err := h.Store.Save(&cfg); err != nil {
			respondError(w, http.StatusInternalServerError, "save failed: " + err.Error())
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
