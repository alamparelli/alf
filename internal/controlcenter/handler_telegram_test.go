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
	h := &TelegramHandler{} // no vault, no env vars

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
	h := &TelegramHandler{}

	body := strings.NewReader(`{"bot_token": "123:abc"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/telegram", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTelegramHandler_PutEmptyBody(t *testing.T) {
	h := &TelegramHandler{}

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPut, "/api/telegram", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTelegramHandler_Delete(t *testing.T) {
	h := &TelegramHandler{} // nil vault — delete is a no-op but should return 200

	req := httptest.NewRequest(http.MethodDelete, "/api/telegram", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTelegramHandler_GetConfiguredViaDockerSecrets(t *testing.T) {
	dir := t.TempDir()
	h := &TelegramHandler{} // no vault

	// Simulate Docker secrets via _FILE env vars.
	tokenFile := filepath.Join(dir, "bot_token")
	chatFile := filepath.Join(dir, "chat_id")
	os.WriteFile(tokenFile, []byte("12345678:ABCDEFGHIJKLMNOP"), 0o600)
	os.WriteFile(chatFile, []byte("999"), 0o600)

	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", tokenFile)
	t.Setenv("TELEGRAM_CHAT_ID_FILE", chatFile)

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
	if masked, ok := resp["bot_token_masked"].(string); ok {
		if !strings.Contains(masked, "...") {
			t.Errorf("expected masked token with '...', got %q", masked)
		}
	} else {
		t.Error("expected bot_token_masked in response")
	}
}

func TestTelegramHandler_MethodNotAllowed(t *testing.T) {
	h := &TelegramHandler{}

	req := httptest.NewRequest(http.MethodPatch, "/api/telegram", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
