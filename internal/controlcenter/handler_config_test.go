package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockConfigStore struct {
	cfg  *Config
	saved *Config
}

func (m *mockConfigStore) Load() (*Config, error) {
	if m.saved != nil {
		return m.saved, nil
	}
	if m.cfg == nil {
		return DefaultConfig(), nil
	}
	return m.cfg, nil
}

func (m *mockConfigStore) Save(cfg *Config) error {
	m.saved = cfg
	return nil
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
	h := &ConfigHandler{Store: store, Notifier: notifier, Event: ReloadConfig}

	body := `{"log_level":"debug","model":"opus","allowed_chat_ids":[],"system_prompt":"","quiet_hours":{"start":0,"end":0},"session_timeout":30,"git_track":true,"git_sweep_interval":5}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if store.saved == nil {
		t.Fatal("expected Save to be called")
	}
	if store.saved.Model != "opus" {
		t.Errorf("saved model: got %q, want 'opus'", store.saved.Model)
	}
	if len(notifier.events) != 1 || notifier.events[0] != ReloadConfig {
		t.Errorf("expected ReloadConfig notification, got %v", notifier.events)
	}
}

func TestConfigHandler_PUT_InvalidModel(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	body := `{"model":"gpt4","log_level":"info"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "invalid model") {
		t.Errorf("error should mention invalid model, got: %s", resp["error"])
	}
}

func TestConfigHandler_PUT_InvalidJSON(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(`{bad json`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestConfigHandler_DELETE_NotAllowed(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	req := httptest.NewRequest("DELETE", "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
