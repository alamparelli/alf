package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
)

// TiersHandler handles GET/PUT /api/tiers.
type TiersHandler struct {
	Store    TierStore
	Notifier Notifier
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, jsonErr("failed to read body"), http.StatusBadRequest)
		return
	}

	var tiers TiersConfig
	if err := json.Unmarshal(body, &tiers); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}

	if err := ValidateTiers(&tiers); err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		return
	}

	if err := h.Store.Save(&tiers); err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	if h.Notifier != nil {
		h.Notifier.Notify(ReloadTiers)
	}

	w.Write([]byte(`{"ok":true}`))
}
