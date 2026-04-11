package controlcenter

import (
	"bufio"
	"fmt"
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

	// Browser top-level navigation (Sec-Fetch-Dest: document) with mobile
	// Bearer token → should set cc_session cookie.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mobileToken)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Sec-Fetch-Dest", "document")
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
	// #271: TTL must be 1h (3600s), not the old 24h.
	if sessionCookie.MaxAge != int(autoIssueSessionTTL/time.Second) {
		t.Errorf("cc_session MaxAge: expected %d (1h), got %d", int(autoIssueSessionTTL/time.Second), sessionCookie.MaxAge)
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

// #271: Accept: text/html alone is no longer enough to trigger an
// auto-session. Only a real top-level navigation (Sec-Fetch-Dest: document)
// should derive a cookie from a Bearer.
func TestAuthMiddleware_BearerNoAutoSessionWithoutSecFetchDest(t *testing.T) {
	ss := NewSessionStore(nil)
	handler := authMiddleware("test-token", ss, nil)(okHandler())

	// GET /foo with Accept: text/html but NO Sec-Fetch-Dest header —
	// previously triggered auto-session; must now be ignored.
	req := httptest.NewRequest("GET", "/foo", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cc_session" {
			t.Error("#271 regression: Accept: text/html alone should not auto-issue a session cookie")
		}
	}
}

// #271: Different Bearer tokens must produce different chatIDs so
// SessionStore.evictOldestLocked cannot evict cross-user. A collision between
// two distinct bearers would reopen the DoS-between-mobile-clients path.
func TestAuthMiddleware_BearerAutoSession_PerBearerIsolation(t *testing.T) {
	ss := NewSessionStore(nil)
	ss.SetMaxSessions(2)
	const tokenA = "bearer-aaaaaaaaaaaaaaaaaaaaaaaa"
	const tokenB = "bearer-bbbbbbbbbbbbbbbbbbbbbbbb"

	// Sanity: distinct bearers must hash to distinct chatIDs, none equal to
	// reserved sentinels (0, -1, -2).
	aID := bearerDerivedChatID(tokenA)
	bID := bearerDerivedChatID(tokenB)
	if aID == bID {
		t.Fatalf("bearerDerivedChatID collision: %d == %d", aID, bID)
	}
	for _, reserved := range []int64{0, -1, -2} {
		if aID == reserved || bID == reserved {
			t.Fatalf("bearerDerivedChatID produced reserved value %d (a=%d b=%d)", reserved, aID, bID)
		}
	}

	handler := authMiddleware(tokenA, ss, nil, func() string { return tokenB })(okHandler())

	issue := func(token string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Sec-Fetch-Dest", "document")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			if c.Name == "cc_session" {
				return c.Value
			}
		}
		t.Fatalf("no cc_session cookie issued for token %q", token)
		return ""
	}

	// Issue maxSessions cookies for bearer A, then two for bearer B.
	a1 := issue(tokenA)
	a2 := issue(tokenA)
	_ = issue(tokenB)
	_ = issue(tokenB)

	// With pre-fix chatID=0 sharing, bearer B's two issues would have evicted
	// a1 and a2 (oldest in the chatID=0 pool). With per-bearer isolation,
	// bearer A's sessions must survive.
	if !ss.Valid(a1) || !ss.Valid(a2) {
		t.Error("#271 regression: bearer B issuance evicted bearer A's auto-sessions (cross-user eviction)")
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

// withTrustedProxyCIDRs overrides the package-level trustedProxyCIDRs for the
// duration of a test. computeTrustedProxyCIDRs() re-reads the env var, so the
// helper can simulate the operator-configured opt-in for private ranges.
func withTrustedProxyCIDRs(t *testing.T, env string) {
	t.Helper()
	saved := trustedProxyCIDRs
	t.Setenv("ALF_TRUSTED_PROXY_CIDRS", env)
	trustedProxyCIDRs = computeTrustedProxyCIDRs()
	t.Cleanup(func() { trustedProxyCIDRs = saved })
}

func TestClientIP_TrustedProxy_HonorsXFF(t *testing.T) {
	// Loopback is trusted by default — a reverse proxy on 127.0.0.1 can set XFF.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345" // loopback = trusted proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.5")

	ip := clientIP(req)
	if ip != "203.0.113.5" {
		t.Errorf("loopback remote: expected XFF value '203.0.113.5', got %q", ip)
	}
}

func TestClientIP_TrustedProxy_DockerBridge_OptIn(t *testing.T) {
	// #272: private ranges are NOT trusted by default anymore. Operators must
	// opt in via ALF_TRUSTED_PROXY_CIDRS. Once opted in, XFF is honored.
	withTrustedProxyCIDRs(t, "172.16.0.0/12")

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.20.0.1:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	ip := clientIP(req)
	if ip != "203.0.113.99" {
		t.Errorf("docker bridge remote (opted in): expected XFF value, got %q", ip)
	}
}

func TestClientIP_PrivateRange_NotTrustedByDefault(t *testing.T) {
	// #272: without explicit opt-in, 10/8 172.16/12 192.168/16 must be treated
	// as untrusted so XFF spoofing from a LAN attacker is ignored.
	for _, remote := range []string{"10.0.0.5:1", "172.20.0.1:1", "192.168.1.10:1"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", "203.0.113.99")

		ip := clientIP(req)
		if ip == "203.0.113.99" {
			host, _, _ := net.SplitHostPort(remote)
			t.Errorf("%s: XFF spoofing accepted by default — should fall back to RemoteAddr %s", remote, host)
		}
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

func TestIsTrustedProxy_LoopbackOnlyByDefault(t *testing.T) {
	// Default policy (#272): only loopback is trusted.
	for _, ip := range []string{"127.0.0.1", "127.1.2.3", "::1"} {
		if !isTrustedProxy(ip) {
			t.Errorf("expected %s to be trusted (loopback default)", ip)
		}
	}
	for _, ip := range []string{"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.100"} {
		if isTrustedProxy(ip) {
			t.Errorf("expected %s NOT trusted by default — opt-in required", ip)
		}
	}
}

func TestIsTrustedProxy_EnvOptIn(t *testing.T) {
	withTrustedProxyCIDRs(t, "10.0.0.0/8, 192.168.0.0/16")
	for _, ip := range []string{"10.0.0.1", "10.255.255.255", "192.168.1.100"} {
		if !isTrustedProxy(ip) {
			t.Errorf("expected %s to be trusted after opt-in", ip)
		}
	}
	// 172.16/12 was NOT added to the opt-in list → still untrusted.
	if isTrustedProxy("172.16.0.1") {
		t.Error("172.16.0.1 should not be trusted (not in opt-in list)")
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

// TestRateLimiter_XFFSpoofRegression is a regression test for #272.
//
// The outer middleware chain in factory.go wraps rateLimiter with
// securityHeadersMiddleware so that X-Forwarded-For / X-Real-IP are stripped
// before the rate limiter reads clientIP(). This test pins that ordering: if
// a future refactor inverts the wrapping, a LAN client in a trusted CIDR
// could once again rotate XFF to escape the anonymous 15/min limit.
func TestRateLimiter_XFFSpoofRegression(t *testing.T) {
	withTrustedProxyCIDRs(t, "10.0.0.0/8") // simulate operator-configured trust

	rl := newRateLimiter(3)
	// Production order: securityHeaders is OUTSIDE rateLimiter.
	chain := securityHeadersMiddleware(rl.middleware(okHandler()))

	// 6 requests from the same LAN IP, each with a different spoofed XFF.
	// With the strip running first, all 6 count against 10.0.0.5 and the
	// 4th+ must be rate limited.
	var okCount, limited int
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest("GET", "/api/ping", nil)
		req.RemoteAddr = "10.0.0.5:12345"
		req.Header.Set("X-Forwarded-For", httpFakeClient(i))
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		switch rec.Code {
		case http.StatusOK:
			okCount++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	if okCount != 3 {
		t.Errorf("expected 3 OK responses (limit=3), got %d", okCount)
	}
	if limited != 3 {
		t.Errorf("expected 3 rate-limited responses, got %d — XFF spoofing bypassed the limiter", limited)
	}
}

// httpFakeClient returns a deterministic dummy public IP for spoofing tests.
func httpFakeClient(i int) string {
	return fmt.Sprintf("203.0.113.%d", i%250+1)
}

// -----------------------------------------------------------------------------
// Regression tests for PENTEST-0.7.8 HIGH-1:
// Sec-Fetch-Dest/Site header forgery bypassed auth + rate limit on /apps/*.
//
// Attack (confirmed against cc.lamparelli.eu on 2026-04-10): a non-browser
// client sending forged `Sec-Fetch-Dest: script|iframe` + `Sec-Fetch-Site:
// same-origin` on /apps/{slug}/... bypassed authMiddleware, reaching
// AppHandler unauthenticated. This allowed:
//   - enumeration of installed apps (via /apps/{slug}/)
//   - unauthenticated dump of manifest.json, app.json, index.html of any app
//   - unlimited requests (no rate limit applied to the bypass path)
//
// Fix: isAppSubResource now also requires Origin: null, a sub-resource file
// extension (not .html/.json), and blocks dest=document|iframe|navigate.
// -----------------------------------------------------------------------------

// helperSubResReq builds a request that would have triggered the old bypass.
func helperSubResReq(method, urlPath string) *http.Request {
	req := httptest.NewRequest(method, urlPath, nil)
	req.Header.Set("Sec-Fetch-Dest", "script")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

func TestIsAppSubResource_Regression_NoOriginNull(t *testing.T) {
	// Forged Sec-Fetch headers but no Origin: null → must NOT be treated as
	// sub-resource (the pentest used this exact shape to bypass auth).
	req := helperSubResReq("GET", "/apps/later/index.js")
	if isAppSubResource(req) {
		t.Fatal("forged Sec-Fetch without Origin: null MUST be rejected")
	}
}

func TestIsAppSubResource_Regression_HTMLRejected(t *testing.T) {
	// HTML document loads must go through normal cookie auth. An attacker
	// could previously enumerate apps via /apps/{slug}/ (→ index.html).
	req := helperSubResReq("GET", "/apps/later/")
	req.Header.Set("Origin", "null")
	if isAppSubResource(req) {
		t.Fatal("HTML document load (no extension) must not be a sub-resource")
	}

	req = helperSubResReq("GET", "/apps/later/index.html")
	req.Header.Set("Origin", "null")
	req.Header.Set("Sec-Fetch-Dest", "iframe")
	if isAppSubResource(req) {
		t.Fatal("index.html with dest=iframe must not be a sub-resource")
	}
}

func TestIsAppSubResource_Regression_JSONRejected(t *testing.T) {
	// manifest.json / app.json dumps were confirmed exploitable in the
	// pentest. These must NOT be served via the sub-resource bypass.
	for _, file := range []string{"manifest.json", "app.json", "config.json"} {
		req := helperSubResReq("GET", "/apps/later/"+file)
		req.Header.Set("Origin", "null")
		if isAppSubResource(req) {
			t.Errorf("%s must not be a sub-resource (leaks app metadata)", file)
		}
	}
}

func TestIsAppSubResource_Regression_DocumentDestRejected(t *testing.T) {
	// dest=document|iframe|navigate must never bypass auth.
	for _, dest := range []string{"document", "iframe", "navigate"} {
		req := helperSubResReq("GET", "/apps/later/foo.js")
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Dest", dest)
		if isAppSubResource(req) {
			t.Errorf("dest=%s must not bypass auth", dest)
		}
	}
}

func TestIsAppSubResource_LegitimateSandboxedFetch(t *testing.T) {
	// A genuine sandboxed iframe sub-resource load — the design case the
	// bypass exists for — must still be recognised.
	cases := []struct {
		ext  string
		dest string
	}{
		{".js", "script"},
		{".mjs", "script"},
		{".css", "style"},
		{".png", "image"},
		{".svg", "image"},
		{".woff2", "font"},
		{".wasm", "script"},
		{".mp4", "video"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/apps/later/asset"+tc.ext, nil)
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Dest", tc.dest)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if !isAppSubResource(req) {
			t.Errorf("legit sub-resource %s (dest=%s) must be allowed", tc.ext, tc.dest)
		}
	}
}

func TestIsAppSubResource_RefererOutsideApps(t *testing.T) {
	// Referer from a non-/apps/ page must be rejected (defense-in-depth).
	req := httptest.NewRequest("GET", "/apps/later/app.js", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Sec-Fetch-Dest", "script")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Referer", "https://evil.example/page")
	if isAppSubResource(req) {
		t.Fatal("external Referer must not be treated as app iframe context")
	}
}

func TestIsAppSubResource_NonGETRejected(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(m, "/apps/later/foo.js", nil)
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Dest", "script")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if isAppSubResource(req) {
			t.Errorf("method %s must not be a sub-resource", m)
		}
	}
}

func TestIsAppSubResource_APIPath_ScriptRejected(t *testing.T) {
	// /apps/{slug}/api/*.js must never bypass auth — app backends serving
	// dynamic JS would be unauth code-exec vector.
	for _, p := range []string{
		"/apps/later/api/items.js",
		"/apps/later/api/bundle.mjs",
		"/apps/later/api/style.css",
		"/apps/later/api/mod.wasm",
		"/apps/later/api/bundle.map",
	} {
		req := httptest.NewRequest("GET", p, nil)
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Dest", "script")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if isAppSubResource(req) {
			t.Errorf("%s under /api/ must never bypass auth", p)
		}
	}
}

func TestIsAppSubResource_APIPath_AssetsAllowed(t *testing.T) {
	// /apps/{slug}/api/*.{jpg,png,woff2,mp4,...} IS allowed to bypass auth
	// so that <img>, <audio>, <video>, @font-face can load assets served
	// dynamically by the app's backend (e.g. bookshelf cover thumbnails).
	cases := []struct {
		path string
		dest string
	}{
		{"/apps/bookshelf/api/covers/1.jpg", "image"},
		{"/apps/bookshelf/api/covers/2.png", "image"},
		{"/apps/bookshelf/api/art/avatar.webp", "image"},
		{"/apps/bookshelf/api/icons/logo.svg", "image"},
		{"/apps/bookshelf/api/fonts/Inter.woff2", "font"},
		{"/apps/bookshelf/api/media/clip.mp4", "video"},
		{"/apps/bookshelf/api/media/ping.mp3", "audio"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Dest", tc.dest)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Referer", "https://cc.example/apps/bookshelf/")
		if !isAppSubResource(req) {
			t.Errorf("%s (dest=%s) must be accepted as an asset sub-resource", tc.path, tc.dest)
		}
	}
}

func TestIsAppSubResource_TagLoad_EmptyOriginAccepted(t *testing.T) {
	// Browsers do NOT send an Origin header for plain <img>, <audio>, <video>,
	// <link> font, or <track> sub-resources — even from a sandboxed null-
	// origin iframe. The bypass MUST accept these requests, otherwise dynamic
	// asset URLs (e.g. <img src="/apps/bookshelf/api/covers/42.jpg">) all
	// 401-out from app iframes. This is the bookshelf regression case.
	cases := []struct {
		path string
		dest string
	}{
		// Static files in app dir
		{"/apps/bookshelf/icon.png", "image"},
		{"/apps/bookshelf/cover.webp", "image"},
		{"/apps/bookshelf/font.woff2", "font"},
		{"/apps/bookshelf/audio.mp3", "audio"},
		{"/apps/bookshelf/clip.mp4", "video"},
		// Dynamic assets via API proxy
		{"/apps/bookshelf/api/covers/1.jpg", "image"},
		{"/apps/bookshelf/api/avatars/u42.png", "image"},
		{"/apps/bookshelf/api/media/song.mp3", "audio"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		// NOTE: deliberately no Origin header — that's the whole point.
		req.Header.Set("Sec-Fetch-Dest", tc.dest)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		if !isAppSubResource(req) {
			t.Errorf("%s (dest=%s, no Origin) must be accepted as a tag-load sub-resource", tc.path, tc.dest)
		}
	}
}

func TestIsAppSubResource_TagLoad_NonAssetDestStillRejected(t *testing.T) {
	// The empty-Origin carve-out is restricted to image/audio/video/font/track.
	// Non-tag-load dest types (script, style, embed, object, ...) MUST still
	// require Origin: null — otherwise the original pentest pattern (forged
	// Sec-Fetch-Dest=script, no real Origin) would re-open unauth source-code
	// dumps from app directories.
	cases := []struct {
		path string
		dest string
	}{
		{"/apps/later/index.js", "script"},
		{"/apps/later/style.css", "style"},
		{"/apps/later/mod.wasm", "script"},
		{"/apps/later/widget.css", "style"},
		{"/apps/later/sw.js", "worker"},
		{"/apps/later/icon.svg", "embed"},
		{"/apps/later/icon.svg", "object"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		// No Origin header — same as a forged sub-resource attack
		req.Header.Set("Sec-Fetch-Dest", tc.dest)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if isAppSubResource(req) {
			t.Errorf("%s (dest=%s, no Origin) must be rejected — non-tag-load dests require Origin: null", tc.path, tc.dest)
		}
	}
}

func TestIsAppSubResource_TagLoad_ThirdPartyOriginRejected(t *testing.T) {
	// A real third-party site loading a CC asset would either send a real
	// Origin (CORS request) or no Origin (regular <img>). The CORS case must
	// still be rejected — only "null" or absent are valid.
	req := httptest.NewRequest("GET", "/apps/bookshelf/api/covers/1.jpg", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if isAppSubResource(req) {
		t.Fatal("third-party Origin must never be treated as a sandboxed sub-resource")
	}
}

func TestIsAppSubResource_APIPath_JSONStillRejected(t *testing.T) {
	// Data endpoints (.json, .xml, .csv, .txt) must stay auth-required even
	// under /api/, regardless of forged sub-resource headers.
	for _, ext := range []string{".json", ".xml", ".csv", ".txt", ".html"} {
		req := httptest.NewRequest("GET", "/apps/later/api/data"+ext, nil)
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if isAppSubResource(req) {
			t.Errorf("%s under /api/ must not bypass auth", ext)
		}
	}
}

// Full auth-middleware integration test reproducing the pentest PoC.
// This is the canonical regression: before the fix, this request got
// next.ServeHTTP (bypass). After the fix, it must get 401.
func TestAuthMiddleware_Regression_PentestSecFetchBypass(t *testing.T) {
	handler := authMiddleware("secret-token", nil, nil)(okHandler())

	// Exact PoC used against cc.lamparelli.eu:
	//   curl -H "Sec-Fetch-Dest: script" -H "Sec-Fetch-Site: same-origin" \
	//        https://cc.lamparelli.eu/apps/later/
	req := httptest.NewRequest("GET", "/apps/later/", nil)
	req.Header.Set("Sec-Fetch-Dest", "iframe")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("PENTEST-0.7.8 HIGH-1 regression: /apps/later/ bypassed auth")
	}

	// Same PoC targeting manifest.json (unauth dump was confirmed).
	req = httptest.NewRequest("GET", "/apps/later/manifest.json", nil)
	req.Header.Set("Sec-Fetch-Dest", "script")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "null")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("PENTEST-0.7.8 HIGH-1 regression: manifest.json bypassed auth")
	}

	// Script sub-resource WITHOUT Origin: null (pentest shape) must 401.
	req = httptest.NewRequest("GET", "/apps/later/app.js", nil)
	req.Header.Set("Sec-Fetch-Dest", "script")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("PENTEST-0.7.8 HIGH-1 regression: /apps/*/*.js without Origin: null bypassed auth")
	}
}
