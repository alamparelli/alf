package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MagicLinkHandler creates a magic login link.
// POST /api/magic-link → {"url":"https://cc.example.com/auth?code=..."}
// Protected by auth middleware (Bearer token required).
type MagicLinkHandler struct {
	Magic       *MagicStore
	ExternalURL string
}

func (h *MagicLinkHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Use chat ID 0 for CLI-generated links (not tied to a Telegram chat).
	code, err := h.Magic.Issue(0, 7*24*time.Hour)
	if err != nil {
		http.Error(w, `{"error":"failed to generate link"}`, http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("%s/auth?code=%s", h.ExternalURL, code)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url})
}
