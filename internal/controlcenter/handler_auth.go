package controlcenter

import (
	"fmt"
	"net/http"
)

// AuthHandler handles magic link authentication.
// GET /auth?code=xxx → consume code, set session cookie, redirect to /.
type AuthHandler struct {
	Magic    *MagicStore
	Sessions *SessionStore
	Secure   bool // set cookie with Secure flag (HTTPS)
}

func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.renderError(w, "Missing code parameter.")
		return
	}

	chatID, ttl, ok := h.Magic.Consume(code)
	if !ok {
		h.renderError(w, "Invalid or expired link. Send /login to get a new one.")
		return
	}

	sessionID, err := h.Sessions.Issue(chatID, ttl)
	if err != nil {
		h.renderError(w, "Internal error creating session.")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "cc_session",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   h.Secure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) renderError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Login Failed</title>
<style>body{background:#1a1a2e;color:#e0e0e0;font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.box{text-align:center;padding:2rem;border:1px solid #333;border-radius:8px;max-width:400px}
h2{color:#ff6b6b}p{color:#aaa}</style></head>
<body><div class="box"><h2>Login Failed</h2><p>%s</p></div></body></html>`, msg)
}
