package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewIPBan_Defaults(t *testing.T) {
	b := newIPBan(0, 0)
	if b.threshold != 10 {
		t.Errorf("expected default threshold 10, got %d", b.threshold)
	}
	if b.duration != 15*time.Minute {
		t.Errorf("expected default duration 15m, got %s", b.duration)
	}
}

func TestNewIPBan_Custom(t *testing.T) {
	b := newIPBan(3, 5*time.Minute)
	if b.threshold != 3 || b.duration != 5*time.Minute {
		t.Errorf("custom values not applied: %+v", b)
	}
}

func TestIPBan_IsBanned_FreshIPIsNotBanned(t *testing.T) {
	b := newIPBan(3, time.Minute)
	if b.isBanned("1.2.3.4") {
		t.Error("fresh IP should not be banned")
	}
}

func TestIPBan_RecordFailureAndBan(t *testing.T) {
	b := newIPBan(2, time.Minute)
	b.recordFailure("1.2.3.4")
	if b.isBanned("1.2.3.4") {
		t.Error("after 1 failure (threshold=2), IP must not be banned yet")
	}
	b.recordFailure("1.2.3.4")
	if !b.isBanned("1.2.3.4") {
		t.Error("after 2 failures, IP must be banned")
	}
	// A different IP remains clean.
	if b.isBanned("5.6.7.8") {
		t.Error("unrelated IP must not be banned")
	}
}

func TestIPBan_RecordSuccessResetsFailures(t *testing.T) {
	b := newIPBan(2, time.Minute)
	b.recordFailure("1.2.3.4")
	b.recordSuccess("1.2.3.4")
	b.recordFailure("1.2.3.4")
	if b.isBanned("1.2.3.4") {
		t.Error("success should have reset failure count")
	}
}

func TestIPBan_ExpiredBanClearsAutomatically(t *testing.T) {
	b := newIPBan(1, time.Millisecond)
	b.recordFailure("1.2.3.4")
	if !b.isBanned("1.2.3.4") {
		t.Fatal("IP should be banned immediately after reaching threshold")
	}
	time.Sleep(5 * time.Millisecond)
	if b.isBanned("1.2.3.4") {
		t.Error("expired ban should have cleared")
	}
}

func TestIPBan_ExtractIP(t *testing.T) {
	b := newIPBan(1, time.Minute)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	if ip := b.extractIP(req); ip != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1, got %q", ip)
	}
}

func TestIPBan_Middleware_BannedIPReturns403(t *testing.T) {
	b := newIPBan(1, time.Minute)
	// Pre-ban the IP.
	b.recordFailure("127.0.0.1")

	handler := b.middleware(okHandler())
	req := httptest.NewRequest("GET", "/api/auth", nil)
	req.RemoteAddr = "127.0.0.1:1111"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("banned IP should get 403, got %d", rec.Code)
	}
}

func TestIPBan_Middleware_TracksFailuresViaStatusCode(t *testing.T) {
	b := newIPBan(2, time.Minute)

	badHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})
	handler := b.middleware(badHandler)

	req := httptest.NewRequest("POST", "/auth/login", nil)
	req.RemoteAddr = "127.0.0.1:5555"

	// First 400 → failure count=1, not yet banned.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if b.isBanned("127.0.0.1") {
		t.Fatal("after 1 failure (threshold=2), must not be banned")
	}

	// Second 400 → triggers ban.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !b.isBanned("127.0.0.1") {
		t.Error("after 2 failures, must be banned")
	}
}

func TestIPBan_Middleware_SuccessStatusResetsFailures(t *testing.T) {
	b := newIPBan(2, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/bad", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	})
	handler := b.middleware(mux)

	// Record 1 failure.
	req := httptest.NewRequest("POST", "/bad", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Record a success on the same IP (303).
	req = httptest.NewRequest("POST", "/ok", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Another failure; still below threshold because success reset.
	req = httptest.NewRequest("POST", "/bad", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if b.isBanned("127.0.0.1") {
		t.Error("success should have reset failure count; IP must not be banned yet")
	}
}
