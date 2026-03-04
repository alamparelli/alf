package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatHandler_PostEmpty(t *testing.T) {
	h := &ChatHandler{Service: newTestChatService(t)}
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestChatHandler_PostInvalidJSON(t *testing.T) {
	h := &ChatHandler{Service: newTestChatService(t)}
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestChatHandler_DeleteNewSession(t *testing.T) {
	h := &ChatHandler{Service: newTestChatService(t)}
	req := httptest.NewRequest("DELETE", "/api/chat", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("expected ok=true, got %v", result["ok"])
	}
}

func TestChatHandler_MethodNotAllowed(t *testing.T) {
	h := &ChatHandler{Service: newTestChatService(t)}
	req := httptest.NewRequest("PATCH", "/api/chat", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestChatHandler_HistoryEmpty(t *testing.T) {
	h := &ChatHandler{Service: newTestChatService(t)}
	req := httptest.NewRequest("GET", "/api/chat?limit=10", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var msgs []ChatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty history, got %d messages", len(msgs))
	}
}

func TestChatHandler_HistoryWithMessages(t *testing.T) {
	svc := newTestChatService(t)
	svc.ChatStore.Append(ChatMessage{
		ID: "msg-1", Role: "user", Text: "hello",
		Timestamp: time.Now().Add(-time.Minute),
	})
	svc.ChatStore.Append(ChatMessage{
		ID: "msg-2", Role: "assistant", Text: "hi",
		Timestamp: time.Now(),
	})

	h := &ChatHandler{Service: svc}
	req := httptest.NewRequest("GET", "/api/chat?limit=50", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var msgs []ChatMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestChatHandler_HistoryPagination(t *testing.T) {
	svc := newTestChatService(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		svc.ChatStore.Append(ChatMessage{
			ID:        NewMessageID(),
			Role:      "user",
			Text:      "msg",
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}

	h := &ChatHandler{Service: svc}
	before := now.Add(5 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest("GET", "/api/chat?limit=3&before="+before, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	var msgs []ChatMessage
	json.Unmarshal(rec.Body.Bytes(), &msgs)
	if len(msgs) != 3 {
		t.Errorf("expected 3, got %d", len(msgs))
	}
}
