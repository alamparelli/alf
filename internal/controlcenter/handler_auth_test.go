package controlcenter

import (
	"net/http"
	"net/http/httptest"
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

func TestAuthHandler_ValidCode(t *testing.T) {
	h, magic, sessions := newTestAuthHandler()

	ttl := 7 * 24 * time.Hour
	code, _ := magic.Issue(123, ttl)
	req := httptest.NewRequest("GET", "/auth?code="+code, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	// Check cookie was set.
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

func TestAuthHandler_MissingCode(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest("GET", "/auth", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_InvalidCode(t *testing.T) {
	h, _, _ := newTestAuthHandler()

	req := httptest.NewRequest("GET", "/auth?code=bogus", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
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
			req := httptest.NewRequest("GET", "/auth?code="+code, nil)
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

func TestAuthHandler_ExpiredCode(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	magic := NewMagicStore(func() time.Time { return now })
	sessions := NewSessionStore(func() time.Time { return now })
	h := &AuthHandler{Magic: magic, Sessions: sessions}

	code, _ := magic.Issue(100, 24*time.Hour)

	// Expire the code.
	expired := now.Add(magicCodeTTL + time.Second)
	magic.nowFn = func() time.Time { return expired }

	req := httptest.NewRequest("GET", "/auth?code="+code, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAuthHandler_DoubleConsume(t *testing.T) {
	h, magic, _ := newTestAuthHandler()

	code, _ := magic.Issue(100, 24*time.Hour)

	// First consume - should succeed.
	req1 := httptest.NewRequest("GET", "/auth?code="+code, nil)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusSeeOther {
		t.Errorf("first use: expected 303, got %d", rec1.Code)
	}

	// Second consume - should fail.
	req2 := httptest.NewRequest("GET", "/auth?code="+code, nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("second use: expected 400, got %d", rec2.Code)
	}
}
