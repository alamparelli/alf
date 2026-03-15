package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// vaultReq sends a request through VaultHandler.ServeHTTP.
func vaultReq(h *VaultHandler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v (body: %s)", err, rec.Body.String())
	}
	return resp["error"]
}

// --- Nil Manager returns 503 ---

func TestVault_NilManager_ListSecrets(t *testing.T) {
	h := &VaultHandler{}
	rec := vaultReq(h, http.MethodGet, "/api/vault/secrets", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if msg := decodeError(t, rec); msg != "vault not available" {
		t.Errorf("unexpected error: %q", msg)
	}
}

func TestVault_NilManager_SetSecret(t *testing.T) {
	h := &VaultHandler{}
	rec := vaultReq(h, http.MethodPost, "/api/vault/secrets", `{"name":"foo","value":"bar"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestVault_NilManager_DeleteSecret(t *testing.T) {
	h := &VaultHandler{}
	rec := vaultReq(h, http.MethodDelete, "/api/vault/secrets/foo", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

// --- handleSetSecret validation (called directly to bypass EnsureAuth) ---

func TestVault_SetSecret_EmptyName(t *testing.T) {
	h := &VaultHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/vault/secrets", strings.NewReader(`{"name":"","value":"bar"}`))
	rec := httptest.NewRecorder()
	h.handleSetSecret(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeError(t, rec); msg != "invalid name" {
		t.Errorf("unexpected error: %q", msg)
	}
}

func TestVault_SetSecret_EmptyValue(t *testing.T) {
	h := &VaultHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/vault/secrets", strings.NewReader(`{"name":"my-secret","value":""}`))
	rec := httptest.NewRecorder()
	h.handleSetSecret(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeError(t, rec); msg != "value required" {
		t.Errorf("unexpected error: %q", msg)
	}
}

func TestVault_SetSecret_PathTraversalName(t *testing.T) {
	cases := []string{
		"../foo",
		"../../etc/passwd",
		"foo/bar",
		"foo/../bar",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			h := &VaultHandler{}
			body := `{"name":"` + name + `","value":"secret"}`
			req := httptest.NewRequest(http.MethodPost, "/api/vault/secrets", strings.NewReader(body))
			rec := httptest.NewRecorder()
			h.handleSetSecret(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for name %q, got %d: %s", name, rec.Code, rec.Body.String())
			}
			if msg := decodeError(t, rec); msg != "invalid name" {
				t.Errorf("unexpected error for name %q: %q", name, msg)
			}
		})
	}
}

func TestVault_SetSecret_InvalidJSON(t *testing.T) {
	h := &VaultHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/vault/secrets", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.handleSetSecret(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeError(t, rec); msg != "invalid request" {
		t.Errorf("unexpected error: %q", msg)
	}
}

// --- isVaultSafeName ---

func TestVaultSafeName(t *testing.T) {
	valid := []string{"my-secret", "API_KEY", "token123", "a.b"}
	for _, name := range valid {
		if !isVaultSafeName(name) {
			t.Errorf("expected %q to be safe", name)
		}
	}

	invalid := []string{"", "../x", "a/b", "a\\b", "..", "."}
	for _, name := range invalid {
		if isVaultSafeName(name) {
			t.Errorf("expected %q to be unsafe", name)
		}
	}
}

// --- DELETE route name validation in ServeHTTP ---

func TestVault_DeleteSecret_PathTraversal_RouteLevel(t *testing.T) {
	// With nil Manager, we get 503 before route matching.
	// This test verifies the 503 is returned consistently for traversal paths too.
	h := &VaultHandler{}
	rec := vaultReq(h, http.MethodDelete, "/api/vault/secrets/../../../etc/passwd", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Unknown route returns 404 (with nil manager it returns 503 first) ---

func TestVault_UnknownRoute_NilManager(t *testing.T) {
	h := &VaultHandler{}
	rec := vaultReq(h, http.MethodGet, "/api/vault/nonexistent", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}
