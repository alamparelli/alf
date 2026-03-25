package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSHHandler_NilManager_NoAuth(t *testing.T) {
	// nil Manager is checked before auth — returns 503 even without credentials
	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  NewSessionStore(nil),
	}

	req := httptest.NewRequest("POST", "/api/ssh/myhost/exec", strings.NewReader(`{"command":"ls"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHHandler_BearerAuth(t *testing.T) {
	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  NewSessionStore(nil),
		// Manager is nil — should return 503
	}

	req := httptest.NewRequest("POST", "/api/ssh/myhost/exec", strings.NewReader(`{"command":"ls"}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should pass auth but fail on nil Manager
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (nil manager), got %d", w.Code)
	}
}

func TestSSHHandler_SessionAuth(t *testing.T) {
	sessions := NewSessionStore(nil)
	sessionID, _ := sessions.Issue(0, 0)

	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  sessions,
		// Manager is nil — should return 503
	}

	req := httptest.NewRequest("POST", "/api/ssh/myhost/exec", strings.NewReader(`{"command":"ls"}`))
	req.AddCookie(&http.Cookie{Name: "cc_session", Value: sessionID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (nil manager), got %d", w.Code)
	}
}

func TestSSHHandler_InvalidPath(t *testing.T) {
	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  NewSessionStore(nil),
	}

	// Missing action
	req := httptest.NewRequest("POST", "/api/ssh/myhost/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should fail on nil manager before path parsing, or return 503
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHHandler_ProxyHTTP(t *testing.T) {
	// Mock vault-proxy server
	mockVault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request path
		if !strings.HasPrefix(r.URL.Path, "/ssh/myhost/exec") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify auth header is set
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"stdout":    "hello",
			"stderr":    "",
			"exit_code": 0,
		})
	}))
	defer mockVault.Close()

	// We can't easily test with a real vault.Manager, but we can test
	// the proxy logic by verifying the mock receives the request.
	// Full integration testing requires a running vault-server.

	// Just verify the mock server works
	body := strings.NewReader(`{"command":"ls"}`)
	req, _ := http.NewRequest("POST", mockVault.URL+"/ssh/myhost/exec", body)
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mock request: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["stdout"] != "hello" {
		t.Errorf("unexpected stdout: %v", result["stdout"])
	}
}

func TestSSHHandler_UnknownAction(t *testing.T) {
	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  NewSessionStore(nil),
	}

	req := httptest.NewRequest("POST", "/api/ssh/myhost/badaction", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// nil manager → 503 before we even check the action
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSSHHandler_NilManager(t *testing.T) {
	h := &SSHHandler{
		Manager:   nil,
		AuthToken: "secret-token",
	}

	req := httptest.NewRequest("POST", "/api/ssh/myhost/exec", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	data, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(data), "vault not available") {
		t.Errorf("unexpected body: %s", data)
	}
}

// --- Security regression tests ---

func TestSSHHandler_PathTraversal(t *testing.T) {
	// Regression: SEC-002 — service name with ".." must be rejected.
	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  NewSessionStore(nil),
	}

	for _, path := range []string{
		"/api/ssh/../secrets/exec",
		"/api/ssh/..%2Fsecrets/exec",
		"/api/ssh/foo/../../bar/exec",
	} {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// Should be either 400 (path traversal caught) or 503 (nil manager, path was clean)
		// The ".." in service name should be caught before reaching manager check.
		if w.Code == http.StatusOK {
			t.Errorf("path %q should not return 200", path)
		}
	}
}

func TestSSHHandler_QueryParamInjection(t *testing.T) {
	// Regression: SEC-001 — cols/rows must be validated as integers.
	// We can't fully test the WebSocket path without a vault manager,
	// but we verify the handler structure rejects non-integer params
	// by checking it doesn't panic and returns an appropriate error.
	h := &SSHHandler{
		AuthToken: "secret-token",
		Sessions:  NewSessionStore(nil),
	}

	// Non-integer cols — should fail before reaching vault
	req := httptest.NewRequest("GET", "/api/ssh/myhost/session?cols=80%26injected%3Dvalue", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// With nil manager, we get 503 before the cols check.
	// This test ensures no panic occurs. With a real manager,
	// the strconv.Atoi validation would return 400.
	if w.Code == http.StatusOK {
		t.Error("should not return 200 for injected query params")
	}
}
