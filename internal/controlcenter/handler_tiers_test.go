package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockTierStore struct {
	tiers   *TiersConfig
	current *TiersConfig
}

func (m *mockTierStore) Load() (*TiersConfig, error) {
	if m.tiers == nil {
		return DefaultTiersConfig(), nil
	}
	return m.tiers, nil
}

func (m *mockTierStore) Current() *TiersConfig {
	if m.current == nil {
		return DefaultTiersConfig()
	}
	return m.current
}

func (m *mockTierStore) Reload() error {
	m.current = m.tiers
	return nil
}

func TestTiersHandler_GET(t *testing.T) {
	store := &mockTierStore{}
	h := &TiersHandler{Store: store}

	req := httptest.NewRequest("GET", "/api/tiers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "default") {
		t.Error("response should contain default tier")
	}
}

func TestTiersHandler_PUT_NotAllowed(t *testing.T) {
	h := &TiersHandler{Store: &mockTierStore{}}

	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestTiersHandler_DELETE_NotAllowed(t *testing.T) {
	h := &TiersHandler{Store: &mockTierStore{}}

	req := httptest.NewRequest("DELETE", "/api/tiers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
