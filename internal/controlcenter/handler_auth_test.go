package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAuthHandler() (*AuthHandler, *MagicStore, *SessionStore) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	magic := NewMagicStore(func() time.Time { return now })
	sessions := NewSessionStore(func() time.Time { return now })
	h := &AuthHandler{Magic: magic, Sessions: sessions}
	return h, magic, sessions
}

// --- GET tests (login form, no consume) ---

func TestAuthHandler_GET_ValidCode(t *testing.T) {
	h, magic, _ := newTestAuthHandler()

	code, _ := magic.Issue(123, 7*24*time.Hour)
	req := httptest.NewRequest("GET", "/auth?code="+code, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<form method="POST"`) {
		t.Error("expected login form in response")
	}
	if !strings.Contains(body, code) {
		t.Error("expected code in hidden form field")
	}
}

func TestAuthHandler_GET_MissingCode(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest("GET", "/auth", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_GET_InvalidCode(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest("GET", "/auth?code=bogus", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_GET_ExpiredCode(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	magic := NewMagicStore(func() time.Time { return now })
	sessions := NewSessionStore(func() time.Time { return now })
	h := &AuthHandler{Magic: magic, Sessions: sessions}

	code, _ := magic.Issue(100, 24*time.Hour)

	expired := now.Add(magicCodeTTL + time.Second)
	magic.nowFn = func() time.Time { return expired }

	req := httptest.NewRequest("GET", "/auth?code="+code, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_GET_DoesNotConsume(t *testing.T) {
	h, magic, _ := newTestAuthHandler()

	code, _ := magic.Issue(123, 7*24*time.Hour)

	// Multiple GETs should all succeed (code not consumed).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/auth?code="+code, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET #%d: expected 200, got %d", i+1, rec.Code)
		}
	}
}

// --- POST tests (consume + session) ---

func TestAuthHandler_POST_ValidCode(t *testing.T) {
	h, magic, sessions := newTestAuthHandler()

	ttl := 7 * 24 * time.Hour
	code, _ := magic.Issue(123, ttl)

	body := strings.NewReader("code=" + code)
	req := httptest.NewRequest("POST", "/auth", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}
	cookie := cookies[0]
	if cookie.Name != "cc_session" {
		t.Errorf("expected cookie name cc_session, got %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if expected := int(ttl.Seconds()); cookie.MaxAge != expected {
		t.Errorf("expected MaxAge %d, got %d", expected, cookie.MaxAge)
	}
	if !sessions.Valid(cookie.Value) {
		t.Error("session should be valid")
	}
}

func TestAuthHandler_POST_MissingCode(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest("POST", "/auth", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_POST_InvalidCode(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	body := strings.NewReader("code=bogus")
	req := httptest.NewRequest("POST", "/auth", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_POST_DoubleConsume(t *testing.T) {
	h, magic, _ := newTestAuthHandler()

	code, _ := magic.Issue(100, 24*time.Hour)

	// First POST - should succeed.
	body1 := strings.NewReader("code=" + code)
	req1 := httptest.NewRequest("POST", "/auth", body1)
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusSeeOther {
		t.Errorf("first POST: expected 303, got %d", rec1.Code)
	}

	// Second POST - should fail (code consumed).
	body2 := strings.NewReader("code=" + code)
	req2 := httptest.NewRequest("POST", "/auth", body2)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("second POST: expected 400, got %d", rec2.Code)
	}
}

func TestAuthHandler_CrawlerThenUser(t *testing.T) {
	h, magic, sessions := newTestAuthHandler()

	ttl := 7 * 24 * time.Hour
	code, _ := magic.Issue(123, ttl)

	// Simulate crawler: multiple GETs.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/auth?code="+code, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("crawler GET #%d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// Simulate user: POST to consume.
	body := strings.NewReader("code=" + code)
	req := httptest.NewRequest("POST", "/auth", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("user POST: expected 303, got %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}
	if !sessions.Valid(cookies[0].Value) {
		t.Error("session should be valid after crawler GETs")
	}
}

func TestAuthHandler_CookieMaxAgeMatchesTTL(t *testing.T) {
	h, magic, _ := newTestAuthHandler()

	for _, tc := range []struct {
		name string
		ttl  time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := magic.Issue(123, tc.ttl)
			body := strings.NewReader("code=" + code)
			req := httptest.NewRequest("POST", "/auth", body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			cookies := rec.Result().Cookies()
			if len(cookies) == 0 {
				t.Fatal("expected session cookie")
			}
			if got, want := cookies[0].MaxAge, int(tc.ttl.Seconds()); got != want {
				t.Errorf("MaxAge = %d, want %d", got, want)
			}
		})
	}
}
