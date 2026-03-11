package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramHandler_GetUnconfigured(t *testing.T) {
	dir := t.TempDir()
	h := &TelegramHandler{ConfigDir: dir}

	req := httptest.NewRequest(http.MethodGet, "/api/telegram", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["configured"] != false {
		t.Errorf("expected configured=false, got %v", resp["configured"])
	}
}

func TestTelegramHandler_PutMissingFields(t *testing.T) {
	dir := t.TempDir()
	h := &TelegramHandler{ConfigDir: dir}

	body := strings.NewReader(`{"bot_token": "123:abc"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/telegram", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTelegramHandler_PutEmptyBody(t *testing.T) {
	dir := t.TempDir()
	h := &TelegramHandler{ConfigDir: dir}

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPut, "/api/telegram", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTelegramHandler_Delete(t *testing.T) {
	dir := t.TempDir()
	h := &TelegramHandler{ConfigDir: dir}

	// Write a config file first.
	os.WriteFile(filepath.Join(dir, "telegram.json"), []byte(`{"bot_token":"x","chat_id":"1"}`), 0o600)

	req := httptest.NewRequest(http.MethodDelete, "/api/telegram", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify file is gone.
	if _, err := os.Stat(filepath.Join(dir, "telegram.json")); !os.IsNotExist(err) {
		t.Error("expected telegram.json to be deleted")
	}
}

func TestTelegramHandler_GetConfigured(t *testing.T) {
	dir := t.TempDir()
	h := &TelegramHandler{ConfigDir: dir}

	// Write a config with a fake token (won't validate against TG API, but tests load logic).
	os.WriteFile(filepath.Join(dir, "telegram.json"), []byte(`{"bot_token":"12345678:ABCDEFGHIJKLMNOP","chat_id":"999"}`), 0o600)

	req := httptest.NewRequest(http.MethodGet, "/api/telegram", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["configured"] != true {
		t.Errorf("expected configured=true, got %v", resp["configured"])
	}
	if resp["chat_id"] != "999" {
		t.Errorf("expected chat_id=999, got %v", resp["chat_id"])
	}
	// Token should be masked.
	if masked, ok := resp["bot_token_masked"].(string); ok {
		if !strings.Contains(masked, "...") {
			t.Errorf("expected masked token with '...', got %q", masked)
		}
	} else {
		t.Error("expected bot_token_masked in response")
	}
}

func TestTelegramHandler_MethodNotAllowed(t *testing.T) {
	h := &TelegramHandler{ConfigDir: t.TempDir()}

	req := httptest.NewRequest(http.MethodPatch, "/api/telegram", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
