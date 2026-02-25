package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockConfigStore struct {
	cfg     *Config
	saveErr error
}

func (m *mockConfigStore) Load() (*Config, error) {
	if m.cfg == nil {
		return DefaultConfig(), nil
	}
	return m.cfg, nil
}

func (m *mockConfigStore) Save(cfg *Config) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.cfg = cfg
	return nil
}

type mockNotifier struct {
	events []ReloadEvent
}

func (m *mockNotifier) Notify(e ReloadEvent) {
	m.events = append(m.events, e)
}

func TestConfigHandler_GET(t *testing.T) {
	store := &mockConfigStore{}
	h := &ConfigHandler{Store: store}

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "log_level") {
		t.Error("response should contain log_level")
	}
}

func TestConfigHandler_PUT_Valid(t *testing.T) {
	store := &mockConfigStore{}
	notifier := &mockNotifier{}
	h := &ConfigHandler{Store: store, Notifier: notifier}

	payload := `{"log_level":"debug","model":"opus"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if store.cfg == nil || store.cfg.LogLevel != "debug" {
		t.Error("config was not saved correctly")
	}
	if store.cfg.Model != "opus" {
		t.Errorf("model: got %q, want 'opus'", store.cfg.Model)
	}
	if len(notifier.events) != 1 || notifier.events[0] != ReloadConfig {
		t.Error("expected ReloadConfig notification")
	}
}

func TestConfigHandler_PUT_UnknownKey(t *testing.T) {
	store := &mockConfigStore{}
	h := &ConfigHandler{Store: store}

	payload := `{"log_level":"info","unknown_field":"val"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown key, got %d", rec.Code)
	}
}

func TestConfigHandler_PUT_InvalidModel(t *testing.T) {
	store := &mockConfigStore{}
	h := &ConfigHandler{Store: store}

	payload := `{"model":"gpt4"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid model, got %d", rec.Code)
	}
}

func TestConfigHandler_MethodNotAllowed(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	req := httptest.NewRequest("DELETE", "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
