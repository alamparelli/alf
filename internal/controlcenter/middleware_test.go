package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func TestAuthMiddleware_ValidBearer(t *testing.T) {
	handler := authMiddleware("test-token", nil, nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidBearer(t *testing.T) {
	handler := authMiddleware("test-token", nil, nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	handler := authMiddleware("test-token", nil, nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExemptPath(t *testing.T) {
	exempt := map[string]bool{"/health": true}
	handler := authMiddleware("test-token", nil, exempt)(okHandler())
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for exempt path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_QueryParam(t *testing.T) {
	handler := authMiddleware("test-token", nil, nil)(okHandler())
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

func TestAuthMiddleware_ValidCookie(t *testing.T) {
	ss := NewSessionStore(nil)
	id, _ := ss.Issue(100, 24*time.Hour)
	handler := authMiddleware("test-token", ss, nil)(okHandler())

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.AddCookie(&http.Cookie{Name: "cc_session", Value: id})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for cookie auth, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidCookie(t *testing.T) {
	ss := NewSessionStore(nil)
	handler := authMiddleware("test-token", ss, nil)(okHandler())

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.AddCookie(&http.Cookie{Name: "cc_session", Value: "bad"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid cookie, got %d", rec.Code)
	}
}

func TestAuthMiddleware_LoginPage(t *testing.T) {
	handler := authMiddleware("test-token", nil, nil)(okHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 login page, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type, got %q", ct)
	}
	body := rec.Body.String()
	if !contains(body, "/login") {
		t.Error("login page should mention /login command")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestAuthMiddleware_LoginPage_NotForAPI(t *testing.T) {
	handler := authMiddleware("test-token", nil, nil)(okHandler())

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// API path should get 401, not login page.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for API path, got %d", rec.Code)
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

func TestCORSMiddleware_AnyOrigin(t *testing.T) {
	handler := corsMiddleware(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://192.168.1.100:9090")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://192.168.1.100:9090" {
		t.Error("expected CORS header to reflect origin")
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
