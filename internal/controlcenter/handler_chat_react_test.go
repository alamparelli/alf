package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatReactHandler_ValidReaction(t *testing.T) {
	svc := newTestChatService(t)

	// Add a message to react to.
	svc.ChatStore.Append(ChatMessage{
		ID: "msg-1", Role: "assistant", Text: "hello",
		Timestamp: time.Now(),
	})

	h := &ChatReactHandler{Service: svc}
	body := `{"msg_id":"msg-1","emoji":"👍"}`
	req := httptest.NewRequest("POST", "/api/chat/react", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result ReactResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
	if !result.OK {
		t.Error("expected ok=true")
	}
}

func TestChatReactHandler_MissingFields(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatReactHandler{Service: svc}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing emoji", `{"msg_id":"msg-1"}`},
		{"missing msg_id", `{"emoji":"👍"}`},
		{"empty body", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/chat/react", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", rec.Code)
			}
		})
	}
}

func TestChatReactHandler_InvalidJSON(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatReactHandler{Service: svc}

	req := httptest.NewRequest("POST", "/api/chat/react", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestChatReactHandler_MethodNotAllowed(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatReactHandler{Service: svc}

	req := httptest.NewRequest("GET", "/api/chat/react", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestChatReactHandler_ReactionStored(t *testing.T) {
	svc := newTestChatService(t)
	svc.ChatStore.Append(ChatMessage{
		ID: "msg-react", Role: "assistant", Text: "test",
		Timestamp: time.Now(),
	})

	h := &ChatReactHandler{Service: svc}
	body := `{"msg_id":"msg-react","emoji":"🔥"}`
	req := httptest.NewRequest("POST", "/api/chat/react", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Verify reaction was stored in chat store.
	msg := svc.ChatStore.Get("msg-react")
	if msg == nil {
		t.Fatal("message not found")
	}
	found := false
	for _, r := range msg.Reactions {
		if r.Emoji == "🔥" && r.From == "user" {
			found = true
			break
		}
	}
	if !found {
		t.Error("reaction not found in message reactions")
	}
}
