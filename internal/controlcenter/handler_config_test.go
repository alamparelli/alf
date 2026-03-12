package controlcenter

import (
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

	body := `{"log_level":"debug","allowed_chat_ids":[],"system_prompt":"","quiet_hours":{"start":0,"end":0},"session_timeout":30,"git_track":true,"git_sweep_interval":5}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if store.saved == nil {
		t.Fatal("expected Save to be called")
	}
	if len(notifier.events) != 1 || notifier.events[0] != ReloadConfig {
		t.Errorf("expected ReloadConfig notification, got %v", notifier.events)
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

func TestConfigHandler_PUT_BackendMissingBaseURL(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	body := `{"log_level":"info","backends":{"test":{"auth":"bearer"}}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "base_url is required") {
		t.Errorf("expected base_url error, got: %s", rec.Body.String())
	}
}

func TestConfigHandler_PUT_BackendInvalidAuth(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	body := `{"log_level":"info","backends":{"test":{"base_url":"https://example.com/v1","auth":"invalid"}}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid auth") {
		t.Errorf("expected auth error, got: %s", rec.Body.String())
	}
}

func TestConfigHandler_PUT_BackendValid(t *testing.T) {
	store := &mockConfigStore{}
	h := &ConfigHandler{Store: store}

	body := `{"log_level":"info","backends":{"openai":{"base_url":"https://api.openai.com/v1","auth":"bearer"}}}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.saved == nil || len(store.saved.Backends) != 1 {
		t.Error("expected backends to be saved")
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
