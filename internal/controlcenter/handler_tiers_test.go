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

func (m *mockTierStore) Save(t *TiersConfig) error {
	m.tiers = t
	m.current = t
	return nil
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

func TestTiersHandler_PUT_Valid(t *testing.T) {
	store := &mockTierStore{}
	notifier := &mockNotifier{}
	h := &TiersHandler{Store: store, Notifier: notifier}

	payload := `{"tiers":[{"name":"fast","model":"haiku","priority":1,"enabled":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.tiers == nil || len(store.tiers.Tiers) != 1 {
		t.Error("tiers not saved")
	}
	if len(notifier.events) != 1 || notifier.events[0] != ReloadTiers {
		t.Error("expected ReloadTiers notification")
	}
}

func TestTiersHandler_PUT_InvalidModel(t *testing.T) {
	store := &mockTierStore{}
	h := &TiersHandler{Store: store}

	payload := `{"tiers":[{"name":"bad","model":"gpt4","priority":1,"enabled":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTiersHandler_PUT_EmptyName(t *testing.T) {
	store := &mockTierStore{}
	h := &TiersHandler{Store: store}

	payload := `{"tiers":[{"name":"","model":"sonnet","priority":0,"enabled":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
