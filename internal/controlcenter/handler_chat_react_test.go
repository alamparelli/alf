package controlcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
)

func TestChatReactHandler_ValidReaction(t *testing.T) {
	svc := newTestChatService(t)

	// Add a message to react to and capture the store-assigned ID.
	msgID := appendTestMessage(t, svc, "test", "assistant", "hello")

	h := &ChatReactHandler{Service: svc}
	body := fmt.Sprintf(`{"msg_id":%q,"emoji":"👍"}`, msgID)
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
	msgID := appendTestMessage(t, svc, "test", "assistant", "test")

	h := &ChatReactHandler{Service: svc}
	body := fmt.Sprintf(`{"msg_id":%q,"emoji":"🔥"}`, msgID)
	req := httptest.NewRequest("POST", "/api/chat/react", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Verify reaction was stored in the memory store.
	ctx := context.Background()
	msg, _ := svc.Memory.GetMessage(ctx, memory.ConvID("test"), memory.MsgID(msgID))
	if msg == nil {
		t.Fatal("message not found")
	}
	found := false
	for _, r := range msg.Reactions {
		if r.Emoji == "🔥" && r.Source == "user" {
			found = true
			break
		}
	}
	if !found {
		t.Error("reaction not found in message reactions")
	}
}
