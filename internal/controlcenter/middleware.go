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
func authMiddleware(token string, sessions *SessionStore, exempt map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt paths.
			if exempt[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}

			// Check Authorization header.
			auth := r.Header.Get("Authorization")
			if token != "" && strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}

			// Check session cookie.
			if sessions != nil {
				if cookie, err := r.Cookie("cc_session"); err == nil && sessions.Valid(cookie.Value) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Log after both auth methods failed.
			if strings.HasPrefix(r.URL.Path, "/api/") {
				log.Printf("[CC] auth fail: ip=%s method=%s path=%s has_auth=%v auth_len=%d token_len=%d",
					clientIP(r), r.Method, r.URL.Path, auth != "", len(auth), len(token))
			}

			// Show login page only for root path — all other unauthenticated paths get 401.
			if (r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/apps/")) && strings.Contains(r.Header.Get("Accept"), "text/html") {
				renderLoginPage(w)
				return
			}

			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		})
	}
}

// renderLoginPage returns a minimal HTML page instructing the user to send /login to the bot.
func renderLoginPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>ALF Control Center — Login</title>
<style>body{background:#1a1a2e;color:#e0e0e0;font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
.box{text-align:center;padding:2rem 3rem;border:1px solid #333;border-radius:8px;max-width:420px}
h2{margin-bottom:.5rem}code{background:#2d2d44;padding:.2em .5em;border-radius:4px;font-size:1.1em}
p{color:#aaa;line-height:1.6}</style></head>
<body><div class="box"><h2>ALF Control Center</h2>
<p>Send <code>/login</code> to your Telegram bot to get a login link.</p>
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
// Same-origin requests (verified via Referer) are also allowed — this covers CC apps
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

// clientIP extracts the client IP, preferring X-Forwarded-For/X-Real-IP from reverse proxies.
func clientIP(r *http.Request) string {
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
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i != -1 {
		ip = ip[:i]
	}
	return ip
}

// isQuietRequest returns true for requests that should not be logged on success.
func isQuietRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	return quietPaths[p] ||
		strings.HasPrefix(p, "/api/") ||
		strings.HasPrefix(p, "/static/") ||
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
	mu       sync.Mutex
	counters map[string]int
	limit    int
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

func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)

		rl.mu.Lock()
		rl.counters[ip]++
		count := rl.counters[ip]
		rl.mu.Unlock()

		if count > rl.limit {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
