package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Without a Manager, every route must short-circuit with 503 — this exercises
// the nil-manager guard in ServeHTTP and the respondError helper, plus the
// early return before any handle* method is reached.
func TestVaultHandler_NoManager_ReturnsServiceUnavailable(t *testing.T) {
	h := &VaultHandler{Manager: nil}

	paths := []struct {
		method, path string
	}{
		{"GET", "/api/vault/status"},
		{"POST", "/api/vault/unlock"},
		{"POST", "/api/vault/lock"},
		{"GET", "/api/vault/services"},
		{"POST", "/api/vault/services"},
		{"DELETE", "/api/vault/services/foo"},
		{"GET", "/api/vault/tokens"},
		{"POST", "/api/vault/tokens"},
		{"GET", "/api/vault/files"},
	}

	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: expected 503, got %d", p.method, p.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "vault not available") {
			t.Errorf("%s %s: expected error body, got %q", p.method, p.path, rec.Body.String())
		}
	}
}
