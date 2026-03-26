package controlcenter

import (
	"encoding/json"
	"net/http"

	"github.com/alamparelli/alf/internal/firewall"
)

// FirewallKillSwitchHandler toggles the network kill switch.
// POST with {"enabled": true/false} to enable/disable.
// GET returns the current state.
type FirewallKillSwitchHandler struct {
	NetTracker  *firewall.NetTracker
	EventBroker *EventBroker
}

func (h *FirewallKillSwitchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.NetTracker == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "nettrack not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]bool{"enabled": h.NetTracker.KillSwitchActive()})

	case http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		h.NetTracker.SetKillSwitch(req.Enabled)
		if h.EventBroker != nil {
			h.EventBroker.Emit(EventFirewall)
		}
		respondJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})

	default:
		methodNotAllowed(w)
	}
}
