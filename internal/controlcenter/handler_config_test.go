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

func TestConfigHandler_PUT_NotificationSoundFalse(t *testing.T) {
	store := &mockConfigStore{}
	h := &ConfigHandler{Store: store}

	body := `{"log_level":"info","notification_sound":false}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.saved == nil {
		t.Fatal("expected Save to be called")
	}
	if store.saved.NotificationSound == nil {
		t.Fatal("NotificationSound should not be nil")
	}
	if *store.saved.NotificationSound != false {
		t.Error("NotificationSound should be false")
	}
}

func TestConfigHandler_PUT_WithoutBackends_PreservesExisting(t *testing.T) {
	existing := DefaultConfig()
	existing.Backends = map[string]BackendConfig{
		"openai": {BaseURL: "https://api.openai.com/v1", Auth: "bearer"},
	}
	store := &mockConfigStore{cfg: existing}
	h := &ConfigHandler{Store: store}

	// PUT without backends field (frontend strips redacted backends).
	body := `{"log_level":"debug"}`
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.saved == nil {
		t.Fatal("expected Save to be called")
	}
	if len(store.saved.Backends) != 1 {
		t.Errorf("expected 1 preserved backend, got %d", len(store.saved.Backends))
	}
	if store.saved.Backends["openai"].BaseURL != "https://api.openai.com/v1" {
		t.Error("preserved backend should retain its base_url")
	}
}

func TestConfigHandler_GET_IncludesNotificationSound(t *testing.T) {
	store := &mockConfigStore{}
	h := &ConfigHandler{Store: store}

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	raw, ok := result["notification_sound"]
	if !ok {
		t.Fatal("response missing notification_sound field")
	}
	if string(raw) != "true" {
		t.Errorf("default notification_sound should be true, got %s", string(raw))
	}
}
