package controlcenter

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// authMiddleware rejects requests without a valid Bearer token.
// Exempt paths (e.g. /health) bypass the check.
func authMiddleware(token string, exempt map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}

			// Check Authorization header.
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") && auth[7:] == token {
				next.ServeHTTP(w, r)
				return
			}

			// Check query param (for dashboard initial load).
			if r.URL.Query().Get("token") == token {
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		})
	}
}

// corsMiddleware restricts origins to localhost:8080.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:8080" || origin == "http://127.0.0.1:8080" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
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
