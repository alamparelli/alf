package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// TiersHandler handles GET and PUT /api/tiers.
type TiersHandler struct {
	Store    TierStore
	Notifier Notifier
	Event    ReloadEvent
}

func (h *TiersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r)
	case http.MethodPut:
		h.put(w, r)
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

func (h *TiersHandler) put(w http.ResponseWriter, r *http.Request) {
	var cfg TiersConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}

	if len(cfg.Tiers) == 0 {
		http.Error(w, jsonErr("at least one tier is required"), http.StatusBadRequest)
		return
	}

	for i, t := range cfg.Tiers {
		if t.Name == "" {
			http.Error(w, jsonErr(fmt.Sprintf("tier %d: name is required", i)), http.StatusBadRequest)
			return
		}
		if !AllowedModels[t.Model] {
			http.Error(w, jsonErr(fmt.Sprintf("tier %q: invalid model %q", t.Name, t.Model)), http.StatusBadRequest)
			return
		}
		if !AllowedEfforts[t.Effort] {
			http.Error(w, jsonErr(fmt.Sprintf("tier %q: invalid effort %q (allowed: low, medium, high)", t.Name, t.Effort)), http.StatusBadRequest)
			return
		}
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
