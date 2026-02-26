package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockConfigStore struct {
	cfg *Config
}

func (m *mockConfigStore) Load() (*Config, error) {
	if m.cfg == nil {
		return DefaultConfig(), nil
	}
	return m.cfg, nil
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

func TestConfigHandler_PUT_NotAllowed(t *testing.T) {
	h := &ConfigHandler{Store: &mockConfigStore{}}

	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
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
