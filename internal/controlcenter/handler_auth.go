package controlcenter

import (
	"fmt"
	"html"
	"net/http"
)

// AuthHandler handles magic link authentication with a two-step flow:
//   - GET /auth?code=xxx → validate code, render login page (crawlers stop here)
//   - POST /auth (code in form body) → consume code, set session cookie, redirect
type AuthHandler struct {
	Magic    *MagicStore
	Sessions *SessionStore
	Secure   bool // set cookie with Secure flag (HTTPS)
}

func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleGet validates the code without consuming it and renders a login page.
func (h *AuthHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		h.renderError(w, "Missing code parameter.")
		return
	}

	if !h.Magic.Peek(code) {
		h.renderError(w, "Invalid or expired link. Send /login to get a new one.")
		return
	}

	h.renderLoginForm(w, code)
}

// handlePost consumes the code, creates a session, and redirects.
func (h *AuthHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
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

func (h *AuthHandler) renderLoginForm(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Login</title>
<style>body{background:#1a1a2e;color:#e0e0e0;font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.box{text-align:center;padding:2rem;border:1px solid #333;border-radius:8px;max-width:400px}
h2{color:#7ec8e3;margin-bottom:.5rem}p{color:#aaa;margin-bottom:1.5rem}
button{background:#7ec8e3;color:#1a1a2e;border:none;padding:.75rem 2rem;border-radius:6px;font-size:1rem;cursor:pointer;font-weight:600}
button:hover{background:#5bb0d0}</style></head>
<body><div class="box"><h2>ALF Control Center</h2><p>Click below to log in.</p>
<form method="POST" action="/auth"><input type="hidden" name="code" value="%s">
<button type="submit">Log in</button></form></div></body></html>`, html.EscapeString(code))
}

func (h *AuthHandler) renderError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Login Failed</title>
<style>body{background:#1a1a2e;color:#e0e0e0;font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.box{text-align:center;padding:2rem;border:1px solid #333;border-radius:8px;max-width:400px}
h2{color:#ff6b6b}p{color:#aaa}</style></head>
<body><div class="box"><h2>Login Failed</h2><p>%s</p></div></body></html>`, html.EscapeString(msg))
}
