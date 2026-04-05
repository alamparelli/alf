package controlcenter

import (
	"bufio"
	"net"
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

func TestAuthMiddleware_QueryParam_Rejected(t *testing.T) {
	// Query param auth was removed for security (token leaks via Referer, browser history).
	handler := authMiddleware("test-token", nil, nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/status?token=test-token", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (query param auth removed), got %d", rec.Code)
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
	if !contains(body, "Not authorized") {
		t.Error("login page should indicate unauthorized access")
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
	handler := corsMiddleware("http://localhost:8080", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8080" {
		t.Error("expected CORS header for localhost:8080")
	}
}

func TestCORSMiddleware_RejectsUnknownOrigin(t *testing.T) {
	handler := corsMiddleware("http://localhost:8080", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS header for unknown origin")
	}
}

func TestCORSMiddleware_EmptyAllowedOrigin(t *testing.T) {
	handler := corsMiddleware("", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://192.168.1.100:9090")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS header when allowedOrigin is empty")
	}
}

func TestCORSMiddleware_TrailingSlashNormalization(t *testing.T) {
	handler := corsMiddleware("http://localhost:8080/", nil)(okHandler())
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8080" {
		t.Error("expected CORS header after trailing slash normalization")
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	handler := corsMiddleware("http://localhost:8080", nil)(okHandler())
	req := httptest.NewRequest("OPTIONS", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
}

// --- statusWriter Hijacker regression tests ---

// mockHijackWriter implements both http.ResponseWriter and http.Hijacker.
type mockHijackWriter struct {
	httptest.ResponseRecorder
	hijacked bool
}

func (m *mockHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	return nil, nil, nil // success sentinel
}

func TestStatusWriter_ImplementsHijacker_WhenUnderlying(t *testing.T) {
	mock := &mockHijackWriter{}
	sw := &statusWriter{ResponseWriter: mock, status: 200}

	// statusWriter must satisfy http.Hijacker when the underlying writer does.
	hijacker, ok := interface{}(sw).(http.Hijacker)
	if !ok {
		t.Fatal("statusWriter does not implement http.Hijacker")
	}

	_, _, err := hijacker.Hijack()
	if err != nil {
		t.Errorf("expected nil error from delegated Hijack, got: %v", err)
	}
	if !mock.hijacked {
		t.Error("expected Hijack to delegate to underlying writer")
	}
}

func TestStatusWriter_Hijack_ErrorsWhenUnderlyingNotHijacker(t *testing.T) {
	// httptest.ResponseRecorder does NOT implement http.Hijacker.
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: 200}

	_, _, err := sw.Hijack()
	if err == nil {
		t.Fatal("expected error when underlying writer is not a Hijacker")
	}
	expected := "underlying ResponseWriter does not implement http.Hijacker"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestStatusWriter_Hijack_InterfaceCheck(t *testing.T) {
	// Compile-time interface satisfaction is implicit via the method,
	// but verify the type assertion works at runtime.
	var w http.ResponseWriter = &statusWriter{ResponseWriter: httptest.NewRecorder()}
	if _, ok := w.(http.Hijacker); !ok {
		t.Fatal("statusWriter should always satisfy http.Hijacker interface")
	}
}

// Verify Flush still works (related interface delegation).
func TestStatusWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: 200}
	// Should not panic even though ResponseRecorder implements Flusher.
	sw.Flush()
	if !rec.Flushed {
		t.Error("expected Flush to delegate to underlying ResponseRecorder")
	}
}

// Ensure loggingMiddleware preserves Hijacker through the full stack.
func TestLoggingMiddleware_PreservesHijacker(t *testing.T) {
	handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter inside loggingMiddleware does not implement http.Hijacker")
			return
		}
		_, _, err := hijacker.Hijack()
		// Underlying is mockHijackWriter, so Hijack succeeds.
		if err != nil {
			t.Errorf("unexpected Hijack error: %v", err)
		}
	}))

	mock := &mockHijackWriter{}
	req := httptest.NewRequest("GET", "/ws/terminal", nil)
	handler.ServeHTTP(mock, req)
	if !mock.hijacked {
		t.Error("expected Hijack to reach underlying writer through loggingMiddleware")
	}
}

// --- Rate limiter auth bypass tests ---

func TestRateLimiter_AuthenticatedBearerBypass(t *testing.T) {
	ss := NewSessionStore(nil)
	rl := newRateLimiter(1).withAuthLimit(100, ss).withToken("my-token")
	handler := rl.middleware(okHandler())

	// First request (anonymous) uses quota.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "1.2.3.4:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first anon request: expected 200, got %d", rec.Code)
	}

	// Second anonymous request should be rate limited.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second anon request: expected 429, got %d", rec.Code)
	}

	// Authenticated request with Bearer token from same IP bypasses limit.
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.RemoteAddr = "1.2.3.4:1"
	req2.Header.Set("Authorization", "Bearer my-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req2)
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated Bearer request: expected 200, got %d", rec.Code)
	}
}

func TestRateLimiter_ExtraTokenBypass(t *testing.T) {
	mobileToken := "mobile-secret-token"
	rl := newRateLimiter(1).withAuthLimit(100, nil).withExtraTokens(func() string {
		return mobileToken
	})
	handler := rl.middleware(okHandler())

	// Exhaust anonymous quota.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "5.5.5.5:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec.Code)
	}

	// Anonymous is now rate limited.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second anon request: expected 429, got %d", rec.Code)
	}

	// Extra token (mobile) bypasses rate limit.
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.RemoteAddr = "5.5.5.5:1"
	req2.Header.Set("Authorization", "Bearer "+mobileToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req2)
	if rec.Code != http.StatusOK {
		t.Errorf("mobile token request: expected 200, got %d", rec.Code)
	}
}

func TestRateLimiter_WrongExtraTokenStillLimited(t *testing.T) {
	rl := newRateLimiter(1).withAuthLimit(100, nil).withExtraTokens(func() string {
		return "real-mobile-token"
	})
	handler := rl.middleware(okHandler())

	// Exhaust quota.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "6.6.6.6:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Wrong token doesn't bypass.
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.RemoteAddr = "6.6.6.6:1"
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req2)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("wrong extra token: expected 429, got %d", rec.Code)
	}
}

func TestRateLimiter_SessionCookieBypass(t *testing.T) {
	ss := NewSessionStore(nil)
	sessionID, _ := ss.Issue(100, 24*time.Hour)
	rl := newRateLimiter(1).withAuthLimit(100, ss)
	handler := rl.middleware(okHandler())

	// Exhaust quota.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "7.7.7.7:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Session cookie bypasses.
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.RemoteAddr = "7.7.7.7:1"
	req2.AddCookie(&http.Cookie{Name: "cc_session", Value: sessionID})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req2)
	if rec.Code != http.StatusOK {
		t.Errorf("session cookie request: expected 200, got %d", rec.Code)
	}
}

func TestRateLimiter_StaticFilesExempt(t *testing.T) {
	rl := newRateLimiter(1)
	handler := rl.middleware(okHandler())

	// Exhaust anonymous quota on an API path.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "8.8.8.8:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first API request: expected 200, got %d", rec.Code)
	}

	// Second API request should be rate limited.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second API request: expected 429, got %d", rec.Code)
	}

	// Static files, root, health, and favicon should still work.
	for _, path := range []string{"/static/style.css", "/", "/health", "/favicon.ico"} {
		req2 := httptest.NewRequest("GET", path, nil)
		req2.RemoteAddr = "8.8.8.8:1"
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req2)
		if rec.Code != http.StatusOK {
			t.Errorf("path %s: expected 200, got %d", path, rec.Code)
		}
	}
}

func TestAuthMiddleware_BearerAutoSession(t *testing.T) {
	ss := NewSessionStore(nil)
	mobileToken := "mobile-token-abc123"
	handler := authMiddleware("primary-token", ss, nil, func() string {
		return mobileToken
	})(okHandler())

	// Browser page navigation with mobile Bearer token → should set cc_session cookie.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mobileToken)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify session cookie was set.
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cc_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected cc_session cookie to be set on Bearer page navigation")
	}

	// Subsequent request with just the session cookie (no Bearer) should work.
	req2 := httptest.NewRequest("GET", "/api/status", nil)
	req2.AddCookie(&http.Cookie{Name: "cc_session", Value: sessionCookie.Value})
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("session cookie from auto-issue: expected 200, got %d", rec2.Code)
	}
}

func TestAuthMiddleware_BearerNoAutoSessionForAPI(t *testing.T) {
	ss := NewSessionStore(nil)
	handler := authMiddleware("test-token", ss, nil)(okHandler())

	// API call with Bearer token → should NOT set session cookie.
	req := httptest.NewRequest("POST", "/api/chat", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == "cc_session" {
			t.Error("should not set session cookie on API call")
		}
	}
}

func TestAuthMiddleware_CcBearerCookieExtraToken(t *testing.T) {
	mobileToken := "mobile-token-xyz"
	handler := authMiddleware("primary-token", nil, nil, func() string {
		return mobileToken
	})(okHandler())

	// cc_bearer cookie with mobile token should authenticate.
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.AddCookie(&http.Cookie{Name: "cc_bearer", Value: mobileToken})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("cc_bearer with mobile token: expected 200, got %d", rec.Code)
	}
}

// Ensure compile-time interface satisfaction.
var _ http.Hijacker = (*statusWriter)(nil)
var _ http.Flusher = (*statusWriter)(nil)

// ---------------------------------------------------------------------------
// SEC-004: clientIP must not trust X-Forwarded-For from untrusted origins
// ---------------------------------------------------------------------------

func TestClientIP_TrustedProxy_HonorsXFF(t *testing.T) {
	// Requests from loopback/private network may carry XFF (reverse proxy scenario).
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345" // loopback = trusted proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.5")

	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Errorf("loopback remote: expected XFF value '203.0.113.5', got %q", ip)
	}
}

func TestClientIP_TrustedProxy_DockerBridge(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.20.0.1:5000" // Docker bridge = trusted
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	ip := clientIP(req)
	if ip != "203.0.113.99" {
		t.Errorf("docker bridge remote: expected XFF value, got %q", ip)
	}
}

func TestClientIP_UntrustedOrigin_IgnoresXFF(t *testing.T) {
	// An external IP spoofing XFF must be ignored — RemoteAddr is used instead.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.10:9999" // public IP = NOT a trusted proxy
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	ip := clientIP(req)
	if ip == "1.2.3.4" {
		t.Error("untrusted remote: XFF header should be ignored, but spoofed IP was accepted")
	}
	if ip != "203.0.113.10" {
		t.Errorf("untrusted remote: expected RemoteAddr '203.0.113.10', got %q", ip)
	}
}

func TestClientIP_UntrustedOrigin_IgnoresXRealIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.7:8080" // public IP
	req.Header.Set("X-Real-IP", "10.0.0.1")

	ip := clientIP(req)
	if ip == "10.0.0.1" {
		t.Error("untrusted remote: X-Real-IP should be ignored")
	}
	if ip != "198.51.100.7" {
		t.Errorf("untrusted remote: expected RemoteAddr, got %q", ip)
	}
}

func TestClientIP_NoHeaders_UsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:4321"

	ip := clientIP(req)
	if ip != "10.0.0.5" {
		t.Errorf("no headers: expected '10.0.0.5', got %q", ip)
	}
}

func TestClientIP_MultiValueXFF_TakesFirst(t *testing.T) {
	// XFF can be "client, proxy1, proxy2" — we want the first (real client).
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1" // trusted proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1, 172.16.0.1")

	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Errorf("multi-value XFF: expected first value '203.0.113.5', got %q", ip)
	}
}

func TestIsTrustedProxy_PrivateRanges(t *testing.T) {
	trusted := []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.100"}
	for _, ip := range trusted {
		if !isTrustedProxy(ip) {
			t.Errorf("expected %s to be a trusted proxy IP", ip)
		}
	}
}

func TestIsTrustedProxy_PublicIPs(t *testing.T) {
	untrusted := []string{"8.8.8.8", "203.0.113.1", "1.1.1.1", "185.0.0.1"}
	for _, ip := range untrusted {
		if isTrustedProxy(ip) {
			t.Errorf("expected %s to NOT be a trusted proxy IP", ip)
		}
	}
}
