package controlcenter

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// subResourceExts is the set of file extensions that a sandboxed null-origin
// iframe legitimately fetches as sub-resources (script, style, image, font,
// media). HTML/JSON documents are excluded on purpose: those must go through
// normal cookie auth via the iframe's document load (which is same-origin
// from the parent and carries cookies).
var subResourceExts = map[string]bool{
	".js": true, ".mjs": true, ".map": true,
	".css":  true,
	".png":  true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".ico": true, ".avif": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".mp3":  true, ".mp4": true, ".webm": true, ".ogg": true, ".wav": true,
	".wasm": true,
}

// assetExts is the narrower subset of subResourceExts allowed to bypass auth
// when the path contains /api/ (i.e. proxied to the app's backend server).
// Restricted to image/audio/video/font so that a sandboxed iframe can load
// <img>, <audio>, <video>, <link> font, <picture>, etc. with direct URLs.
//
// Excluded on purpose vs. subResourceExts: .js, .mjs, .css, .wasm, .map.
// Scripts and styles served dynamically by app backends must still go through
// authenticated AlfSDK.fetch() — letting them bypass auth here would open
// unauth code execution and source-map leaks on user-generated content.
var assetExts = map[string]bool{
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".ico": true, ".avif": true,
	// Fonts
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	// Audio / video
	".mp3": true, ".mp4": true, ".webm": true, ".ogg": true, ".wav": true,
}

// ctxKeyAppTokenSlug stores the slug extracted from a validated app Bearer token.
// Used by handlers (e.g., BashHandler) to cross-check against Referer-derived slug.
type ctxKeyAppTokenSlug struct{}

// AppTokenSlugFromContext returns the app slug from a validated Bearer token, if any.
func AppTokenSlugFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(ctxKeyAppTokenSlug{}).(string); ok {
		return s
	}
	return ""
}

// authMethod indicates which authentication method succeeded.
type authMethod int

const (
	authNone    authMethod = iota
	authBearer             // primary or extra Bearer token
	authCookie             // cc_bearer cookie
	authSession            // cc_session cookie
)

// checkRequestAuth checks whether a request carries valid authentication via
// Bearer token (primary or extra), cc_bearer cookie, or session cookie.
// This is the single source of truth for request authentication, used by both
// the middleware stack and handlers registered outside it (Terminal, SSH).
func checkRequestAuth(r *http.Request, token string, sessions *SessionStore, extraTokenFns []func() string) authMethod {
	// Check Authorization header against primary token.
	auth := r.Header.Get("Authorization")
	if token != "" && strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
		return authBearer
	}

	// Check Authorization header against extra tokens (e.g. mobile API token).
	if strings.HasPrefix(auth, "Bearer ") {
		bearer := auth[7:]
		for _, fn := range extraTokenFns {
			if et := fn(); et != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(et)) == 1 {
				return authBearer
			}
		}
	}

	// Check cc_bearer cookie against primary and extra tokens.
	if cookie, err := r.Cookie("cc_bearer"); err == nil {
		cv := cookie.Value
		if token != "" && subtle.ConstantTimeCompare([]byte(cv), []byte(token)) == 1 {
			return authCookie
		}
		for _, fn := range extraTokenFns {
			if et := fn(); et != "" && subtle.ConstantTimeCompare([]byte(cv), []byte(et)) == 1 {
				return authCookie
			}
		}
	}

	// Check session cookie.
	if sessions != nil {
		if cookie, err := r.Cookie("cc_session"); err == nil && sessions.Valid(cookie.Value) {
			return authSession
		}
	}

	return authNone
}

// authMiddleware rejects requests without a valid Bearer token or session cookie.
// Exempt paths (e.g. /health, /auth) bypass the check.
// For unauthenticated browser requests to GET /, it returns a login page (200) instead of 401.
// extraTokenFns are optional callbacks that return additional valid tokens (e.g. mobile API token from vault).
//
// When a valid Bearer token (primary or mobile) authenticates a browser page navigation
// (GET with Accept: text/html) and no session cookie exists, a session is auto-created.
// This enables mobile WebViews: the initial load carries the Bearer header, the session
// cookie is set, and subsequent JS fetch() calls authenticate via cookie automatically.
// stripToolsSocketHeader removes the X-Tools-Socket header from incoming TCP
// requests to prevent external clients from forging it to bypass auth.
// The header is only legitimate when set by ToolsProxy on Unix socket connections.
func stripToolsSocketHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-Tools-Socket")
		next.ServeHTTP(w, r)
	})
}

// isAppSubResource returns true if the request is a browser sub-resource load
// (script, style, image, font, etc.) from a sandboxed app iframe. Sandboxed
// iframes at /apps/{slug}/ are loaded with sandbox="allow-scripts ..." (no
// allow-same-origin), giving them an opaque "null" origin. Sub-resource fetches
// from that context cannot attach cookies OR Bearer tokens, so the auth
// middleware exempts them based on request shape.
//
// SECURITY: Sec-Fetch-* headers are NOT a trust signal on their own — any HTTP
// client can set them arbitrarily. The bypass is gated by multiple conditions
// that are hard to satisfy *together* outside a real sandboxed iframe:
//
//  1. Origin: null  — sandboxed iframes without allow-same-origin have opaque
//     origin. Non-browser clients can forge it, but it's the canonical marker
//     and aligns with corsMiddleware which already special-cases "null".
//  2. File extension must be a real sub-resource type (script, style, image,
//     font, media, wasm). HTML/JSON are excluded: document loads of the iframe
//     itself are fetched same-origin by the parent window and MUST carry
//     cookies (normal auth path). This closes the enumeration vector where an
//     attacker could hit /apps/{slug}/ or /apps/{slug}/manifest.json with
//     forged Sec-Fetch headers to list installed apps.
//  3. Sec-Fetch-Dest must match a sub-resource type (not document/iframe).
//  4. Sec-Fetch-Site must be present.
//
// NOTE: The rate limiter still exempts sub-resources (apps may legitimately
// load many assets per page load). DoS via fully-forged conditions is a
// residual risk but significantly reduced — the auth bypass (enumeration,
// file dumps) is closed, and real sandboxed iframes are the only "honest"
// users of this path.
func isAppSubResource(r *http.Request) bool {
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/apps/") {
		return false
	}

	// 1. Extension must be a real sub-resource type — never HTML/JSON/etc.
	ext := strings.ToLower(path.Ext(r.URL.Path))
	if !subResourceExts[ext] {
		return false
	}

	// 1b. For API-proxied paths (/apps/{slug}/api/...), narrow the allowed
	// extensions to image/audio/video/font only. This lets <img>, <audio>,
	// <video>, and @font-face load assets that an app backend serves
	// dynamically (e.g. user-uploaded covers, transcoded media) while still
	// blocking unauth access to .js/.css/.wasm/.map — those must go through
	// the authenticated Bearer path via AlfSDK.fetch().
	if strings.Contains(r.URL.Path, "/api/") && !assetExts[ext] {
		return false
	}

	// 3. Sec-Fetch headers must be present.
	dest := r.Header.Get("Sec-Fetch-Dest")
	site := r.Header.Get("Sec-Fetch-Site")
	if dest == "" || site == "" {
		return false
	}

	// 2. Origin gate. Two valid cases:
	//   (a) Origin: null  — fetch()/XHR from a sandboxed null-origin iframe
	//       (AlfSDK.api / AlfSDK.fetch always take this path).
	//   (b) Empty Origin + tag-load dest (image/audio/video/font/track)
	//       — browsers NEVER send an Origin header for plain <img>, <audio>,
	//         <video>, <link rel=preload as=font>, or <track> sub-resources,
	//         even from a sandboxed null-origin iframe. Without this carve-
	//         out, <img src="/apps/X/api/cover.jpg"> from an app iframe gets
	//         401, breaking dynamic asset loading entirely.
	//
	// The carve-out is restricted to tag-load dests so the original pentest
	// pattern (forged Sec-Fetch-Dest=script + empty Origin) stays rejected:
	// script/style/wasm/etc. would expose source code unauthenticated, while
	// image/audio/video/font expose at most opaque media content the browser
	// can't read pixel/sample data from cross-origin. The residual risk is
	// that a third-party site can hot-link an app's media if it knows the
	// exact slug + path — equivalent to default web behavior for any public
	// image URL, and bounded to media that can't leak data via the browser.
	origin := r.Header.Get("Origin")
	isTagLoad := dest == "image" || dest == "audio" || dest == "video" ||
		dest == "font" || dest == "track"
	if origin != "null" && !(origin == "" && isTagLoad) {
		return false
	}

	// 4. Reject document/iframe/navigate — those are not sub-resources and must
	// go through normal cookie auth via the parent window's same-origin fetch.
	if dest == "document" || dest == "iframe" || dest == "navigate" {
		return false
	}

	// 5. If Referer is present, it must point to an /apps/ path.
	ref := r.Header.Get("Referer")
	if ref != "" && !strings.Contains(ref, "/apps/") {
		return false
	}

	// Any remaining sub-resource dest (script, style, image, font, audio,
	// video, worker, track, embed, object, etc.) with any Sec-Fetch-Site is
	// accepted — the null-origin + extension + dest combination is what
	// distinguishes a real sandboxed sub-resource from a crafted request.
	return true
}

func authMiddleware(token string, sessions *SessionStore, exempt map[string]bool, extraTokenFns ...func() string) func(http.Handler) http.Handler {
	return authMiddlewareWithAppTokens(token, sessions, nil, exempt, extraTokenFns...)
}

func authMiddlewareWithAppTokens(token string, sessions *SessionStore, appTokens *AppTokenStore, exempt map[string]bool, extraTokenFns ...func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt paths and CORS preflights (OPTIONS carry no credentials).
			if exempt[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/static/") || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			// Exempt app sub-resource loads from sandboxed iframes.
			if isAppSubResource(r) {
				next.ServeHTTP(w, r)
				return
			}

			// Tools socket: internal marker set by ToolsProxy (socket access = auth).
			// Only trusted on Unix socket connections — the TCP server strips this
			// header via stripToolsSocketHeader() to prevent external forgery.
			if r.Header.Get("X-Tools-Socket") == "1" {
				next.ServeHTTP(w, r)
				return
			}

			result := checkRequestAuth(r, token, sessions, extraTokenFns)
			if result != authNone {
				if result == authBearer {
					autoIssueSession(w, r, sessions)
				}
				next.ServeHTTP(w, r)
				return
			}

			// App token auth: sandboxed iframes use Bearer app tokens.
			// Tokens are slug-scoped and accepted on:
			//   /apps/{slug}/...    — static files + API proxy
			//   /api/apps/{slug}/...— storage, upload, errors, permissions
			//   /api/bash           — shell commands (permission-checked by handler)
			//   /api/app-action     — cross-app actions
			if appTokens != nil {
				if bearer := extractAppBearerToken(r); bearer != "" {
					if _, ok := appTokens.Validate(bearer); ok {
						path := r.URL.Path
						// Slug-scoped routes: verify token slug matches
						if strings.HasPrefix(path, "/apps/") || strings.HasPrefix(path, "/api/apps/") {
							var prefix string
							if strings.HasPrefix(path, "/apps/") {
								prefix = "/apps/"
							} else {
								prefix = "/api/apps/"
							}
							reqSlug := strings.TrimPrefix(path, prefix)
							if idx := strings.IndexByte(reqSlug, '/'); idx >= 0 {
								reqSlug = reqSlug[:idx]
							}
							tokenSlug, _ := appTokens.Validate(bearer)
							if reqSlug == tokenSlug {
								next.ServeHTTP(w, r)
								return
							}
						}
						// Non-scoped routes: propagate token slug in context
						// so handlers can cross-check against Referer-derived slug.
						if path == "/api/bash" || path == "/api/app-action" {
							tokenSlug, _ := appTokens.Validate(bearer)
							ctx := context.WithValue(r.Context(), ctxKeyAppTokenSlug{}, tokenSlug)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
				}
			}

			// Log after all auth methods failed.
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				log.Printf("[CC] auth fail: ip=%s method=%s path=%s has_auth=%v auth_len=%d token_len=%d",
					clientIP(r), r.Method, r.URL.Path, auth != "", len(auth), len(token))
			}

			// Show login page only for root path - all other unauthenticated paths get 401.
			if (r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/apps/")) && strings.Contains(r.Header.Get("Accept"), "text/html") {
				renderLoginPage(w)
				return
			}

			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		})
	}
}

// autoIssueSession creates a session cookie when a Bearer-authenticated browser request
// arrives without an existing session. This bridges Bearer auth (mobile app) to cookie auth
// (WebView JS), so subsequent fetch() calls work automatically.
func autoIssueSession(w http.ResponseWriter, r *http.Request, sessions *SessionStore) {
	if sessions == nil {
		return
	}
	// Only issue on browser page navigations, not API calls.
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return
	}
	// Skip if already has a valid session.
	if cookie, err := r.Cookie("cc_session"); err == nil && sessions.Valid(cookie.Value) {
		return
	}
	sessionID, err := sessions.Issue(0, 24*time.Hour)
	if err != nil {
		log.Printf("[CC] auto-session issue failed: %v", err)
		return
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	autoSameSite := http.SameSiteLaxMode
	if secure {
		autoSameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cc_session",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   secure,
		SameSite: autoSameSite,
	})
	log.Printf("[CC] auto-session issued for Bearer auth from %s", clientIP(r))
}

// renderLoginPage returns a minimal HTML page for unauthenticated visitors.
func renderLoginPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Override restrictive default CSP for the login page HTML.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>ALF Control Center</title>
<style>body{background:#1a1a2e;color:#e0e0e0;font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.box{text-align:center;padding:2rem 3rem;border:1px solid #333;border-radius:8px;max-width:420px}
h2{margin-bottom:.5rem}
p{color:#aaa;line-height:1.6}</style></head>
<body><div class="box"><h2>ALF Control Center</h2>
<p>Not authorized.</p>
</div></body></html>`))
}

// corsMiddleware only allows CORS from the configured origin (derived from externalURL).
// If allowedOrigin is empty, no CORS headers are set (same-origin only).
// Sandboxed iframes (origin "null") are allowed on app routes when they carry a valid app token.
func corsMiddleware(allowedOrigin string, appTokens *AppTokenStore) func(http.Handler) http.Handler {
	allowedOrigin = strings.TrimRight(allowedOrigin, "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")

			allowed := origin != "" && allowedOrigin != "" && origin == allowedOrigin

			// Sandboxed iframes have origin "null" — allow on app routes only.
			// For preflight (OPTIONS) we must allow without token (browsers don't send auth on preflight).
			// For actual requests, validate the Bearer app token to prevent abuse from non-browser callers.
			// Sub-resource loads (already authenticated via Sec-Fetch-Dest in authMiddleware) also get CORS.
			if !allowed && origin == "null" {
				p := r.URL.Path
				isAppRoute := strings.HasPrefix(p, "/apps/") || strings.HasPrefix(p, "/api/apps/") ||
					strings.HasPrefix(p, "/static/") ||
					p == "/api/bash" || p == "/api/app-action"
				if isAppRoute {
					if strings.HasPrefix(p, "/static/") {
						// Static assets are public — always allow CORS for sandboxed iframes.
						allowed = true
					} else if r.Method == http.MethodOptions {
						allowed = true
					} else if isAppSubResource(r) {
						// Already authenticated by authMiddleware via Sec-Fetch-Dest —
						// grant CORS so the browser doesn't block the response.
						allowed = true
					} else if appTokens != nil {
						if bearer := extractAppBearerToken(r); bearer != "" {
							if _, ok := appTokens.Validate(bearer); ok {
								allowed = true
							}
						}
					}
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// jsonMiddleware sets Content-Type: application/json for /api/ paths.
func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
		}
		next.ServeHTTP(w, r)
	})
}

// csrfMiddleware requires a X-Requested-With header on state-changing requests.
// HTML forms cannot set custom headers, so this prevents cross-site form submissions.
// JavaScript from allowed origins can set this header, and CORS preflight enforces the origin check.
// Same-origin requests (verified via Referer) are also allowed - this covers CC apps
// served at /apps/* which have their own JS context without the fetch override.
func csrfMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("X-Requested-With") == "" {
					// Allow Bearer-token authenticated requests (API clients, not browsers).
					if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
						next.ServeHTTP(w, r)
						return
					}
					// Allow same-origin requests verified via Referer header.
					// CC apps at /apps/* make fetch calls without X-Requested-With.
					if allowedOrigin != "" && strings.HasPrefix(r.Header.Get("Referer"), allowedOrigin) {
						next.ServeHTTP(w, r)
						return
					}
					http.Error(w, `{"error":"missing X-Requested-With header"}`, http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// appIsolationMiddleware restricts API access from app iframes.
// When a request originates from an app iframe (Referer contains /apps/),
// only the app's own endpoints are allowed. This prevents a malicious app
// from accessing vault, config, other apps' APIs, or any CC admin endpoints.
func appIsolationMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			referer := r.Header.Get("Referer")
			if referer == "" || !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			// Extract the app slug from the Referer, if any.
			// Referer: https://cc.example.com/apps/my-app/... → "my-app"
			appSlug := extractAppSlugFromReferer(r)
			if appSlug == "" {
				// Not from an app iframe — allow all API access.
				next.ServeHTTP(w, r)
				return
			}

			// App iframe detected — restrict to allowed endpoints only.
			path := r.URL.Path

			// Allow: app's own proxy API
			if strings.HasPrefix(path, "/apps/"+appSlug+"/api/") {
				next.ServeHTTP(w, r)
				return
			}

			// Allow: cross-app actions (handler validates caller and target)
			if path == "/api/app-action" {
				next.ServeHTTP(w, r)
				return
			}

			// Allow: bash endpoint (permission checked by handler)
			if path == "/api/bash" {
				next.ServeHTTP(w, r)
				return
			}

			// Allow: app storage, upload, errors (handler validates app ownership via Referer)
			// Routes: /api/apps/{slug}/storage, /api/apps/{slug}/upload, /api/apps/{slug}/errors
			if strings.HasPrefix(path, "/api/apps/"+appSlug+"/") {
				next.ServeHTTP(w, r)
				return
			}

			// Allow: SSE events (read-only)
			if path == "/api/events" {
				next.ServeHTTP(w, r)
				return
			}

			// Block everything else from app iframes.
			log.Printf("[CC] app-isolation: blocked %s %s from app %s", r.Method, path, appSlug)
			http.Error(w, `{"error":"forbidden: app isolation"}`, http.StatusForbidden)
		})
	}
}

// securityHeadersMiddleware sets security headers on every response.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SEC-007: Strip incoming forwarded headers to prevent host injection.
		// These may be set by external reverse proxies or spoofed by clients.
		r.Header.Del("X-Forwarded-Host")
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")
		r.Header.Del("X-Real-Ip")

		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// Allow iframing for /apps/ (user apps loaded in AppFrame), deny for everything else.
		if strings.HasPrefix(r.URL.Path, "/apps/") {
			h.Set("X-Frame-Options", "SAMEORIGIN")
		} else {
			h.Set("X-Frame-Options", "DENY")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// CSP for HTML responses is set by DashboardHandler with page-specific policy.
		// For non-HTML (API, static), a restrictive default prevents any rendering.
		if !strings.HasPrefix(r.URL.Path, "/apps/") {
			if existing := h.Get("Content-Security-Policy"); existing == "" {
				h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Polling endpoints excluded from logging to reduce noise.
var quietPaths = map[string]bool{
	"/api/logs":   true,
	"/api/status": true,
	"/health":     true,
}

// loggingMiddleware logs each request, skipping high-frequency polling and
// static asset requests that succeed.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if sw.status < 400 && isQuietRequest(r) {
			return
		}
		log.Printf("[CC] %s %s %s %d %s", clientIP(r), r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// trustedProxyCIDRs are networks whose X-Forwarded-For/X-Real-IP headers we trust.
// Only connections from these addresses may override the client IP.
// Includes Docker default bridge, common overlay networks, and loopback.
var trustedProxyCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",   // loopback
		"10.0.0.0/8",    // Docker / private class A
		"172.16.0.0/12", // Docker default bridge range
		"192.168.0.0/16", // private class C
		"::1/128",       // IPv6 loopback
	}
	var nets []*net.IPNet
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

func isTrustedProxy(remoteIP string) bool {
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxyCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP extracts the client IP.
// X-Forwarded-For / X-Real-IP are only trusted when the direct connection comes
// from a known trusted proxy (loopback or private network), preventing IP spoofing
// by external clients.
func clientIP(r *http.Request) string {
	remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
	if remoteHost == "" {
		remoteHost = r.RemoteAddr
	}

	if isTrustedProxy(remoteHost) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// X-Forwarded-For may contain multiple IPs; first is the client.
			if i := strings.Index(xff, ","); i != -1 {
				return strings.TrimSpace(xff[:i])
			}
			return xff
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return xri
		}
	}

	return remoteHost
}

// quietPostPaths are POST endpoints excluded from logging on success (high-frequency app calls).
var quietPostPaths = map[string]bool{
	"/api/bash": true,
}

// isQuietRequest returns true for requests that should not be logged on success.
func isQuietRequest(r *http.Request) bool {
	p := r.URL.Path
	if r.Method == http.MethodPost {
		return quietPostPaths[p]
	}
	if r.Method != http.MethodGet {
		return false
	}
	return quietPaths[p] ||
		strings.HasPrefix(p, "/api/") ||
		strings.HasPrefix(p, "/static/") ||
		strings.HasPrefix(p, "/apps/") ||
		p == "/" ||
		p == "/favicon.ico"
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher so SSE streaming works through the logging middleware.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker so WebSocket upgrades work through middleware.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// ipBan tracks failed attempts and bans IPs that exceed the threshold.
type ipBan struct {
	mu        sync.Mutex
	failures  map[string]int       // IP → failure count
	banned    map[string]time.Time // IP → ban expiry
	threshold int                  // failures before ban
	duration  time.Duration        // ban duration
}

func newIPBan(threshold int, duration time.Duration) *ipBan {
	if threshold <= 0 {
		threshold = 10
	}
	if duration <= 0 {
		duration = 15 * time.Minute
	}
	return &ipBan{
		failures:  make(map[string]int),
		banned:    make(map[string]time.Time),
		threshold: threshold,
		duration:  duration,
	}
}

func (b *ipBan) extractIP(r *http.Request) string {
	return clientIP(r)
}

// isBanned returns true if the IP is currently banned.
func (b *ipBan) isBanned(ip string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	expiry, ok := b.banned[ip]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(b.banned, ip)
		delete(b.failures, ip)
		return false
	}
	return true
}

// recordFailure increments the failure count and bans the IP if threshold is exceeded.
func (b *ipBan) recordFailure(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures[ip]++
	if b.failures[ip] >= b.threshold {
		b.banned[ip] = time.Now().Add(b.duration)
		log.Printf("[CC] IP banned: %s (duration=%s)", ip, b.duration)
	}
}

// recordSuccess clears the failure count for an IP.
func (b *ipBan) recordSuccess(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, ip)
}

// middleware wraps a handler with IP ban enforcement and failure tracking.
// It checks the response status: 400 = failure (invalid/expired code), 303 = success.
func (b *ipBan) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := b.extractIP(r)
		if b.isBanned(ip) {
			http.Error(w, `{"error":"too many failed attempts, try again later"}`, http.StatusForbidden)
			return
		}

		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)

		if sw.status == http.StatusBadRequest {
			b.recordFailure(ip)
		} else if sw.status == http.StatusSeeOther {
			b.recordSuccess(ip)
		}
	})
}

// rateLimitMiddleware limits requests per IP per minute.
type rateLimiter struct {
	mu             sync.Mutex
	counters       map[string]int
	limit          int
	authLimit      int              // higher limit for authenticated requests (0 = same as limit)
	sessions       *SessionStore    // optional — used to detect authenticated requests
	token          string           // optional — Bearer token also gets authLimit
	extraTokenFns  []func() string  // optional — additional valid tokens (e.g. mobile API token)
	appTokens      *AppTokenStore   // optional — HMAC app tokens also get authLimit
}

func newRateLimiter(limit int) *rateLimiter {
	rl := &rateLimiter{
		counters: make(map[string]int),
		limit:    limit,
	}
	go func() {
		for {
			time.Sleep(time.Minute)
			rl.mu.Lock()
			rl.counters = make(map[string]int)
			rl.mu.Unlock()
		}
	}()
	return rl
}

// withAuthLimit sets a higher limit for authenticated requests (session cookie or Bearer token).
func (rl *rateLimiter) withAuthLimit(authLimit int, sessions *SessionStore) *rateLimiter {
	rl.authLimit = authLimit
	rl.sessions = sessions
	return rl
}

// withToken allows Bearer-token requests to use the authLimit.
func (rl *rateLimiter) withToken(token string) *rateLimiter {
	rl.token = token
	return rl
}

// withExtraTokens adds additional token providers (e.g. mobile API token from vault).
func (rl *rateLimiter) withExtraTokens(fns ...func() string) *rateLimiter {
	rl.extraTokenFns = fns
	return rl
}

// withAppTokens allows HMAC-signed app tokens to use the authLimit.
func (rl *rateLimiter) withAppTokens(tokens *AppTokenStore) *rateLimiter {
	rl.appTokens = tokens
	return rl
}

func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never rate-limit static assets, the login page, or health checks — these
		// must remain reachable even when a misbehaving app floods API endpoints.
		p := r.URL.Path
		if r.Method == http.MethodOptions || strings.HasPrefix(p, "/static/") || p == "/" || p == "/health" || p == "/favicon.ico" ||
			isAppSubResource(r) {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)

		rl.mu.Lock()
		rl.counters[ip]++
		count := rl.counters[ip]
		rl.mu.Unlock()

		effective := rl.limit
		if rl.authLimit > 0 {
			authenticated := false
			if rl.sessions != nil {
				if cookie, err := r.Cookie("cc_session"); err == nil && rl.sessions.Valid(cookie.Value) {
					authenticated = true
				}
			}
			if !authenticated {
				if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
					bearer := auth[7:]
					// Check primary token.
					if rl.token != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(rl.token)) == 1 {
						authenticated = true
					}
					// Check extra tokens (e.g. mobile API token from vault).
					if !authenticated {
						for _, fn := range rl.extraTokenFns {
							if et := fn(); et != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(et)) == 1 {
								authenticated = true
								break
							}
						}
					}
					// Check HMAC app tokens (sandboxed iframe auth).
					if !authenticated && rl.appTokens != nil {
						if _, ok := rl.appTokens.Validate(bearer); ok {
							authenticated = true
						}
					}
				}
				// Check cc_bearer cookie (mobile WebView sub-resources).
				if !authenticated {
					if cookie, err := r.Cookie("cc_bearer"); err == nil {
						cv := cookie.Value
						if rl.token != "" && subtle.ConstantTimeCompare([]byte(cv), []byte(rl.token)) == 1 {
							authenticated = true
						}
						if !authenticated {
							for _, fn := range rl.extraTokenFns {
								if et := fn(); et != "" && subtle.ConstantTimeCompare([]byte(cv), []byte(et)) == 1 {
									authenticated = true
									break
								}
							}
						}
					}
				}
			}
			if authenticated {
				effective = rl.authLimit
			}
		}

		if count > effective {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
