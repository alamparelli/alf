package controlcenter

import (
	"log"
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
			if token != "" && strings.HasPrefix(auth, "Bearer ") && auth[7:] == token {
				next.ServeHTTP(w, r)
				return
			}

			// Check query param (for dashboard initial load).
			if token != "" && r.URL.Query().Get("token") == token {
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

			// For browser GET / requests, show login page instead of 401.
			if r.Method == http.MethodGet && r.URL.Path == "/" && strings.Contains(r.Header.Get("Accept"), "text/html") {
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

// corsMiddleware restricts origins to localhost:8080.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:8080" || origin == "http://127.0.0.1:8080" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
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

// loggingMiddleware logs each request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Printf("[CC] %s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
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
		ip := r.RemoteAddr
		if i := strings.LastIndex(ip, ":"); i != -1 {
			ip = ip[:i]
		}

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
