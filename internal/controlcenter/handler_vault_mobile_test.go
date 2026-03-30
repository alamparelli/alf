package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alamparelli/alf/internal/vault"
)

// fakeVaultServer simulates the vault-server file API endpoints used by the
// mobile token handlers: GET/POST /files (upload), GET /files/<name>,
// DELETE /files/<name>, and GET /tokens (used by EnsureAuth).
func fakeVaultHandler() (http.Handler, *fakeVaultStore) {
	store := &fakeVaultStore{files: make(map[string]string)}
	mux := http.NewServeMux()

	// EnsureAuth calls ListTokens; return success so the handler proceeds.
	mux.HandleFunc("/tokens", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
	})

	// Upload file (multipart form) -- used by Manager.SetSecret.
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			name := r.FormValue("name")
			file, _, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			store.Set(name, string(data))
			w.WriteHeader(http.StatusCreated)
			return
		}
		// GET /files -- list files (used by ListFiles).
		w.Header().Set("Content-Type", "application/json")
		var files []map[string]string
		for name := range store.All() {
			files = append(files, map[string]string{"name": name})
		}
		if files == nil {
			files = []map[string]string{}
		}
		json.NewEncoder(w).Encode(files)
	})

	// GET/DELETE /files/<name>
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/files/")
		switch r.Method {
		case http.MethodGet:
			val, ok := store.Get(name)
			if !ok {
				http.Error(w, `{"error":"not found"}`, 404)
				return
			}
			w.Write([]byte(val))
		case http.MethodDelete:
			store.Delete(name)
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	return mux, store
}

// fakeVaultStore is a thread-safe in-memory key-value store.
type fakeVaultStore struct {
	mu    sync.Mutex
	files map[string]string
}

func (s *fakeVaultStore) Set(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = value
}

func (s *fakeVaultStore) Get(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.files[name]
	return v, ok
}

func (s *fakeVaultStore) Delete(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, name)
}

func (s *fakeVaultStore) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]string, len(s.files))
	for k, v := range s.files {
		cp[k] = v
	}
	return cp
}

// newTestVaultHandler creates a VaultHandler backed by a fake vault-server on a Unix socket.
func newTestVaultHandler(t *testing.T) (*VaultHandler, *fakeVaultStore, func()) {
	t.Helper()
	handler, store := fakeVaultHandler()
	mgr, cleanup := vault.NewTestManager(t, handler, "test-admin-token")
	h := &VaultHandler{Manager: mgr}
	return h, store, cleanup
}

// --- Handler Tests ---

func TestMobileTokenGenerate(t *testing.T) {
	h, store, cleanup := newTestVaultHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/vault/mobile-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should return ok=true and a token.
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	token, ok := resp["token"].(string)
	if !ok || token == "" {
		t.Fatal("expected non-empty token in response")
	}

	// Token should be 64-char hex (32 random bytes).
	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars: %s", len(token), token)
	}
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("token contains non-hex character: %c", c)
			break
		}
	}

	// Token should be stored in vault under "cc_mobile_token".
	stored, ok := store.Get("cc_mobile_token")
	if !ok {
		t.Fatal("token not stored in vault")
	}
	if stored != token {
		t.Errorf("stored token %q does not match returned token %q", stored, token)
	}
}

func TestMobileTokenGet_Exists(t *testing.T) {
	h, store, cleanup := newTestVaultHandler(t)
	defer cleanup()

	// Pre-populate a token.
	store.Set("cc_mobile_token", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	req := httptest.NewRequest(http.MethodGet, "/api/vault/mobile-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["exists"] != true {
		t.Errorf("expected exists=true, got %v", resp["exists"])
	}

	masked, ok := resp["token_masked"].(string)
	if !ok || masked == "" {
		t.Fatal("expected non-empty token_masked")
	}
	// Masked token should NOT be the full token.
	fullToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	if masked == fullToken {
		t.Error("token_masked should be obfuscated, not the full token")
	}
	// Should contain "..." (obfuscation).
	if !strings.Contains(masked, "...") {
		t.Errorf("expected masked token to contain '...', got %q", masked)
	}
}

func TestMobileTokenGet_NotExists(t *testing.T) {
	h, _, cleanup := newTestVaultHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/vault/mobile-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["exists"] != false {
		t.Errorf("expected exists=false, got %v", resp["exists"])
	}
	if _, hasToken := resp["token_masked"]; hasToken {
		t.Error("should not include token_masked when token does not exist")
	}
}

func TestMobileTokenRevoke(t *testing.T) {
	h, store, cleanup := newTestVaultHandler(t)
	defer cleanup()

	// Pre-populate a token.
	store.Set("cc_mobile_token", "some-token-value")

	req := httptest.NewRequest(http.MethodDelete, "/api/vault/mobile-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}

	// Token should be removed from vault.
	if _, exists := store.Get("cc_mobile_token"); exists {
		t.Error("token should have been deleted from vault")
	}
}

// --- Auth Middleware Tests ---

func TestMobileTokenAuth(t *testing.T) {
	mobileToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	tokenFn := func() string { return mobileToken }

	handler := authMiddleware("primary-token", nil, nil, tokenFn)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+mobileToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for valid mobile token, got %d", rec.Code)
	}
}

func TestMobileTokenAuth_Invalid(t *testing.T) {
	mobileToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	tokenFn := func() string { return mobileToken }

	handler := authMiddleware("primary-token", nil, nil, tokenFn)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-mobile-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid mobile token, got %d", rec.Code)
	}
}

func TestMobileTokenAuth_EmptyExtraToken(t *testing.T) {
	// When the extra token function returns empty string, it should not match anything.
	tokenFn := func() string { return "" }

	handler := authMiddleware("primary-token", nil, nil, tokenFn)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when extra token is empty, got %d", rec.Code)
	}
}

func TestMobileTokenAuth_PrimaryStillWorks(t *testing.T) {
	// Primary token should still be accepted when extra token fn is present.
	tokenFn := func() string { return "mobile-token" }

	handler := authMiddleware("primary-token", nil, nil, tokenFn)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer primary-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for primary token, got %d", rec.Code)
	}
}

// --- GetMobileToken helper ---

func TestGetMobileToken_NilManager(t *testing.T) {
	result := GetMobileToken(nil)
	if result != "" {
		t.Errorf("expected empty string for nil manager, got %q", result)
	}
}
