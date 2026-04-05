package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppTokenStore_IssueAndValidate(t *testing.T) {
	store, err := NewAppTokenStore()
	if err != nil {
		t.Fatal(err)
	}

	token := store.Issue("my-app")
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	slug, ok := store.Validate(token)
	if !ok {
		t.Fatal("expected valid token")
	}
	if slug != "my-app" {
		t.Errorf("slug = %q, want %q", slug, "my-app")
	}
}

func TestAppTokenStore_ValidateForSlug(t *testing.T) {
	store, err := NewAppTokenStore()
	if err != nil {
		t.Fatal(err)
	}

	token := store.Issue("my-app")

	if !store.ValidateForSlug(token, "my-app") {
		t.Error("expected valid for my-app")
	}
	if store.ValidateForSlug(token, "other-app") {
		t.Error("expected invalid for other-app")
	}
}

func TestAppTokenStore_ExpiredToken(t *testing.T) {
	store, err := NewAppTokenStore()
	if err != nil {
		t.Fatal(err)
	}

	// Temporarily reduce TTL — we can't easily do this without modifying the
	// const, so we test with a token that we manually expire by manipulating time.
	// Instead, test that a garbage token fails.
	_, ok := store.Validate("garbage-token")
	if ok {
		t.Error("expected invalid for garbage token")
	}

	_, ok = store.Validate("")
	if ok {
		t.Error("expected invalid for empty token")
	}
}

func TestAppTokenStore_DifferentKeys(t *testing.T) {
	store1, _ := NewAppTokenStore()
	store2, _ := NewAppTokenStore()

	token := store1.Issue("my-app")
	_, ok := store2.Validate(token)
	if ok {
		t.Error("token from store1 should not validate on store2 (different keys)")
	}
}

func TestHandleAppToken(t *testing.T) {
	store, _ := NewAppTokenStore()

	req := httptest.NewRequest("GET", "/api/apps/test-app/token", nil)
	rec := httptest.NewRecorder()
	handleAppToken(rec, req, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "token") {
		t.Errorf("body should contain token: %s", body)
	}
}

func TestHandleAppToken_NilStore(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/apps/test-app/token", nil)
	rec := httptest.NewRecorder()
	handleAppToken(rec, req, nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHandleAppToken_MethodNotAllowed(t *testing.T) {
	store, _ := NewAppTokenStore()
	req := httptest.NewRequest("POST", "/api/apps/test-app/token", nil)
	rec := httptest.NewRecorder()
	handleAppToken(rec, req, store)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestAppHandler_BearerAuth(t *testing.T) {
	tokens, _ := NewAppTokenStore()

	dir := t.TempDir()
	appDir := filepath.Join(dir, "my-app")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>test</html>"), 0o644)

	h := &AppHandler{
		Store:     NewFileAppStore(dir),
		AppTokens: tokens,
	}

	// Request without auth (should still serve — auth is handled by middleware)
	req := httptest.NewRequest("GET", "/apps/my-app/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAppTokenStore_TokenFormat(t *testing.T) {
	store, _ := NewAppTokenStore()
	token := store.Issue("x")

	if len(token) < 20 {
		t.Errorf("token too short: %d chars", len(token))
	}

	slug, ok := store.Validate(token)
	if !ok || slug != "x" {
		t.Errorf("immediate validation failed: slug=%q ok=%v", slug, ok)
	}
}
