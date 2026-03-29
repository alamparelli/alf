package controlcenter

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

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

func authMiddleware(token string, sessions *SessionStore, exempt map[string]bool, extraTokenFns ...func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt paths.
			if exempt[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/static/") {
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

			// Check Authorization header against primary token.
			auth := r.Header.Get("Authorization")
			if token != "" && strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
				autoIssueSession(w, r, sessions)
				next.ServeHTTP(w, r)
				return
			}

			// Check Authorization header against extra tokens (e.g. mobile API token).
			if strings.HasPrefix(auth, "Bearer ") {
				bearer := auth[7:]
				for _, fn := range extraTokenFns {
					if et := fn(); et != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(et)) == 1 {
						autoIssueSession(w, r, sessions)
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// Check cc_bearer cookie against primary and extra tokens.
			if cookie, err := r.Cookie("cc_bearer"); err == nil {
				cv := cookie.Value
				if token != "" && subtle.ConstantTimeCompare([]byte(cv), []byte(token)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
				for _, fn := range extraTokenFns {
					if et := fn(); et != "" && subtle.ConstantTimeCompare([]byte(cv), []byte(et)) == 1 {
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// Check session cookie.
			if sessions != nil {
				if cookie, err := r.Cookie("cc_session"); err == nil && sessions.Valid(cookie.Value) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Log after all auth methods failed.
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
	http.SetCookie(w, &http.Cookie{
		Name:     "cc_session",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteNoneMode,
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
func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	allowedOrigin = strings.TrimRight(allowedOrigin, "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			if origin != "" && allowedOrigin != "" && origin == allowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
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

// securityHeadersMiddleware sets security headers on every response.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never rate-limit static assets, the login page, or health checks — these
		// must remain reachable even when a misbehaving app floods API endpoints.
		p := r.URL.Path
		if strings.HasPrefix(p, "/static/") || p == "/" || p == "/health" || p == "/favicon.ico" {
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
				// No rate limit for authenticated users (games, apps make many requests).
				next.ServeHTTP(w, r)
				return
			}
		}

		if count > effective {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
