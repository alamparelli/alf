package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func TestAuthMiddleware_ValidBearer(t *testing.T) {
	handler := authMiddleware("test-token", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidBearer(t *testing.T) {
	handler := authMiddleware("test-token", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	handler := authMiddleware("test-token", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExemptPath(t *testing.T) {
	exempt := map[string]bool{"/health": true}
	handler := authMiddleware("test-token", exempt)(okHandler())
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for exempt path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_QueryParam(t *testing.T) {
	handler := authMiddleware("test-token", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status?token=test-token", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for query param auth, got %d", rec.Code)
	}
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3)
	handler := rl.middleware(okHandler())

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// 4th request should be rate limited.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	rl := newRateLimiter(1)
	handler := rl.middleware(okHandler())

	// First IP uses its quota.
	req1 := httptest.NewRequest("GET", "/api/test", nil)
	req1.RemoteAddr = "1.1.1.1:1"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("IP1 first request: expected 200, got %d", rec1.Code)
	}

	// Second IP still has quota.
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.RemoteAddr = "2.2.2.2:2"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("IP2 first request: expected 200, got %d", rec2.Code)
	}
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	handler := corsMiddleware(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8080" {
		t.Error("expected CORS header for localhost:8080")
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	handler := corsMiddleware(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set CORS header for unknown origin")
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	handler := corsMiddleware(okHandler())
	req := httptest.NewRequest("OPTIONS", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
}
