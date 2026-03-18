package controlcenter

import (
	"fmt"
	"net/http"
	"strconv"
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
		methodNotAllowed(w)
		return
	}

	// Duration: default 7 days, override with ?days=N (max 90).
	// days=0 means no expiration (10-year session).
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 0 && n <= 90 {
			days = n
		}
	}

	var sessTTL time.Duration
	if days == 0 {
		sessTTL = 10 * 365 * 24 * time.Hour // ~10 years
	} else {
		sessTTL = time.Duration(days) * 24 * time.Hour
	}

	// Use chat ID 0 for CLI-generated links (not tied to a Telegram chat).
	code, err := h.Magic.Issue(0, sessTTL)
	if err != nil {
		http.Error(w, `{"error":"failed to generate link"}`, http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("%s/auth?code=%s", h.ExternalURL, code)
	respondJSON(w, http.StatusOK, map[string]string{"url": url})
}
