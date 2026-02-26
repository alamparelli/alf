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
	saved   *TiersConfig
}

func (m *mockTierStore) Load() (*TiersConfig, error) {
	if m.tiers == nil {
		return DefaultTiersConfig(), nil
	}
	return m.tiers, nil
}

func (m *mockTierStore) Save(cfg *TiersConfig) error {
	m.saved = cfg
	m.tiers = cfg
	m.current = cfg
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
	if !strings.Contains(rec.Body.String(), "instant") {
		t.Error("response should contain instant tier")
	}
}

func TestTiersHandler_PUT_Valid(t *testing.T) {
	store := &mockTierStore{}
	notifier := &mockNotifier{}
	h := &TiersHandler{Store: store, Notifier: notifier, Event: ReloadTiers}

	body := `{"tiers":[{"name":"fast","model":"haiku","priority":1,"enabled":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.saved == nil {
		t.Fatal("expected tiers to be saved")
	}
	if len(store.saved.Tiers) != 1 || store.saved.Tiers[0].Name != "fast" {
		t.Errorf("unexpected saved tiers: %+v", store.saved.Tiers)
	}
	if len(notifier.events) != 1 || notifier.events[0] != ReloadTiers {
		t.Errorf("expected ReloadTiers event, got %v", notifier.events)
	}
}

func TestTiersHandler_PUT_EmptyTiers(t *testing.T) {
	h := &TiersHandler{Store: &mockTierStore{}}

	body := `{"tiers":[]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTiersHandler_PUT_InvalidModel(t *testing.T) {
	h := &TiersHandler{Store: &mockTierStore{}}

	body := `{"tiers":[{"name":"bad","model":"gpt-4","priority":0,"enabled":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid model") {
		t.Errorf("expected invalid model error, got: %s", rec.Body.String())
	}
}

func TestTiersHandler_PUT_MissingName(t *testing.T) {
	h := &TiersHandler{Store: &mockTierStore{}}

	body := `{"tiers":[{"name":"","model":"sonnet","priority":0,"enabled":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTiersHandler_PUT_InvalidEffort(t *testing.T) {
	h := &TiersHandler{Store: &mockTierStore{}}

	body := `{"tiers":[{"name":"bad","model":"sonnet","priority":0,"enabled":true,"effort":"extreme"}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid effort") {
		t.Errorf("expected invalid effort error, got: %s", rec.Body.String())
	}
}

func TestTiersHandler_PUT_WithNewFields(t *testing.T) {
	store := &mockTierStore{}
	h := &TiersHandler{Store: store}

	body := `{"tiers":[{"name":"heavy","model":"sonnet","priority":2,"enabled":true,"routable":true,"router_label":"File changes","write_capable":true,"effort":"medium","force_command":true}]}`
	req := httptest.NewRequest("PUT", "/api/tiers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.saved == nil {
		t.Fatal("expected tiers to be saved")
	}
	tier := store.saved.Tiers[0]
	if !tier.Routable || !tier.WriteCapable || !tier.ForceCommand {
		t.Errorf("new fields not saved: routable=%v write=%v force=%v", tier.Routable, tier.WriteCapable, tier.ForceCommand)
	}
	if tier.Effort != "medium" {
		t.Errorf("expected effort 'medium', got %q", tier.Effort)
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
