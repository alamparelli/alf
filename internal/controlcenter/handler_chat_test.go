package controlcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/runtime/comms"
	"github.com/alamparelli/alf/internal/memory"
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

// Regression guard for #310: POST without conv_id must be rejected so the
// message is never silently dropped from persistence.
func TestChatHandler_PostRequiresConvID(t *testing.T) {
	h := &ChatHandler{Service: newTestChatService(t)}
	body := `{"message":"hi"}`
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing conv_id, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "conv_id") {
		t.Errorf("expected error to mention conv_id, got %q", rec.Body.String())
	}
}

// Regression guard for #310: StartJob persists the user message
// synchronously so it survives a refresh even if the provider call is
// cancelled or slow. Before the fix, persistence happened inside
// engine.Process (async) and a quick refresh could lose the message.
func TestChatService_StartJobPersistsUserMsgBeforeProvider(t *testing.T) {
	svc := newTestChatService(t)
	ctx := context.Background()
	_ = svc.Memory.EnsureConv(ctx, "conv-310", "", "cc")

	// Block Ask so the async provider never completes in this test.
	release := make(chan struct{})
	defer close(release)
	svc.askOverride = func(ctx context.Context, _ ChatRequest, _ func(ChatEvent)) error {
		<-release
		return nil
	}

	svc.StartJob(ChatRequest{ConvID: "conv-310", Message: "save me"})

	// StartJob returns after the synchronous insert, so history must contain
	// the message immediately.
	msgs, err := svc.Memory.ListMessages(ctx, "conv-310", memory.ListOpts{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var found bool
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "save me" {
			found = true
			if m.CreatedAt == 0 {
				t.Error("user message CreatedAt should not be zero")
			}
			break
		}
	}
	if !found {
		t.Fatalf("user message not persisted synchronously; got %d messages", len(msgs))
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

func TestChatHandler_DeleteFiresOnSessionEnd(t *testing.T) {
	svc := newTestChatService(t)

	// Wire a minimal engine with OnSessionEnd hook.
	var firedWith string
	eng := &comms.ChatEngine{
		Sessions:     svc.Sessions,
		ContextDir:   svc.ContextDir,
		OnSessionEnd: func(sid string) { firedWith = sid },
	}
	svc.Engine = eng

	// Set the session under the key the engine will actually use
	// (ChannelID("cc:-1").SessionKey() hashes "cc:-1", not raw apiChatID).
	chID := comms.ChannelID("cc:" + fmt.Sprint(apiChatID))
	svc.Sessions.Set(chID.SessionKey(), "test-session-xyz")

	h := &ChatHandler{Service: svc}
	req := httptest.NewRequest("DELETE", "/api/chat", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if firedWith != "test-session-xyz" {
		t.Errorf("expected OnSessionEnd('test-session-xyz'), got %q", firedWith)
	}
}

func TestChatHandler_DeleteNoFireWhenNoSession(t *testing.T) {
	svc := newTestChatService(t)

	fired := false
	eng := &comms.ChatEngine{
		Sessions:     svc.Sessions,
		ContextDir:   svc.ContextDir,
		OnSessionEnd: func(sid string) { fired = true },
	}
	svc.Engine = eng

	h := &ChatHandler{Service: svc}
	req := httptest.NewRequest("DELETE", "/api/chat", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if fired {
		t.Error("OnSessionEnd should not fire when no session was active")
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
	_ = appendTestMessage(t, svc, "test", "user", "hello")
	_ = appendTestMessage(t, svc, "test", "assistant", "hi")

	h := &ChatHandler{Service: svc}
	req := httptest.NewRequest("GET", "/api/chat?limit=50&conv_id=test", nil)
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
	for i := 0; i < 10; i++ {
		_ = appendTestMessage(t, svc, "test", "user", "msg")
	}

	// TODO(#336): before-timestamp pagination dropped in memory migration
	// (see chat_service.History). This test now verifies limit only.
	h := &ChatHandler{Service: svc}
	req := httptest.NewRequest("GET", "/api/chat?limit=3&conv_id=test", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	var msgs []HistoryMessage
	json.Unmarshal(rec.Body.Bytes(), &msgs)
	if len(msgs) != 3 {
		t.Errorf("expected 3, got %d", len(msgs))
	}
}

func TestChatActiveHandler_GetReturnsActiveConvID(t *testing.T) {
	svc := newTestChatService(t)
	svc.SetActiveConvID("conv-abc")

	h := &ChatActiveHandler{Service: svc, EventBroker: NewEventBroker()}
	req := httptest.NewRequest("GET", "/api/chat/active", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
	if result["active_conv_id"] != "conv-abc" {
		t.Errorf("expected conv-abc, got %q", result["active_conv_id"])
	}
}

func TestChatActiveHandler_GetEmpty(t *testing.T) {
	svc := newTestChatService(t)

	h := &ChatActiveHandler{Service: svc, EventBroker: NewEventBroker()}
	req := httptest.NewRequest("GET", "/api/chat/active", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result map[string]string
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["active_conv_id"] != "" {
		t.Errorf("expected empty active_conv_id, got %q", result["active_conv_id"])
	}
}

func TestChatActiveHandler_PutSetsActive(t *testing.T) {
	svc := newTestChatService(t)
	broker := NewEventBroker()

	h := &ChatActiveHandler{Service: svc, EventBroker: broker}
	body := `{"conv_id":"conv-xyz","client_id":"browser-1"}`
	req := httptest.NewRequest("PUT", "/api/chat/active", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify it was persisted.
	if got := svc.CurrentConvID(); got != "conv-xyz" {
		t.Errorf("expected conv-xyz, got %q", got)
	}

	// Verify DB persistence survives clearing in-memory state.
	svc.lastChatConv = ""
	if got := svc.CurrentConvID(); got != "conv-xyz" {
		t.Errorf("expected conv-xyz from DB fallback, got %q", got)
	}
}

func TestChatActiveHandler_PutEmptyConvID(t *testing.T) {
	h := &ChatActiveHandler{Service: newTestChatService(t), EventBroker: NewEventBroker()}
	body := `{"conv_id":"","client_id":"browser-1"}`
	req := httptest.NewRequest("PUT", "/api/chat/active", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty conv_id, got %d", rec.Code)
	}
}

func TestChatActiveHandler_PutConvIDTooLong(t *testing.T) {
	h := &ChatActiveHandler{Service: newTestChatService(t), EventBroker: NewEventBroker()}
	longID := strings.Repeat("x", 65)
	body := `{"conv_id":"` + longID + `","client_id":"ok"}`
	req := httptest.NewRequest("PUT", "/api/chat/active", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for conv_id > 64, got %d", rec.Code)
	}
}

func TestChatActiveHandler_PutClientIDTooLong(t *testing.T) {
	h := &ChatActiveHandler{Service: newTestChatService(t), EventBroker: NewEventBroker()}
	longClient := strings.Repeat("c", 65)
	body := `{"conv_id":"ok","client_id":"` + longClient + `"}`
	req := httptest.NewRequest("PUT", "/api/chat/active", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for client_id > 64, got %d", rec.Code)
	}
}

func TestChatActiveHandler_MethodNotAllowed(t *testing.T) {
	h := &ChatActiveHandler{Service: newTestChatService(t), EventBroker: NewEventBroker()}
	req := httptest.NewRequest("DELETE", "/api/chat/active", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
