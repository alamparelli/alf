package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/tooling"
)

// Regression lock for provider paths that the ai step (step 4) of
// milestone 0.7.9 will move but that are not touched by the SSE /
// API integration tests. Pins the tooling adapter glue, the prompt
// cache tagging policy, the CLI JSON-result parser, the LLM log
// writer, and the generic-provider registry slot.

// ----- ToolingExecutorAdapter -----------------------------------------

type fakeNativeTool struct {
	name string
	run  func(ctx context.Context, args string) (string, error)
}

func (f fakeNativeTool) ToolName() string { return f.name }
func (f fakeNativeTool) Schema() tooling.ToolSchema {
	return tooling.ToolSchema{Name: f.name, Description: "test"}
}
func (f fakeNativeTool) Run(ctx context.Context, args string) (string, error) {
	return f.run(ctx, args)
}

func TestToolingExecutorAdapter_Execute_HappyPath(t *testing.T) {
	exec := &tooling.Executor{}
	exec.RegisterNative(fakeNativeTool{
		name: "echo",
		run: func(_ context.Context, args string) (string, error) {
			return "echo-output-for-" + args, nil
		},
	})

	adapter := NewToolingExecutorAdapter(exec)

	result := adapter.Execute(context.Background(), ToolCallRequest{
		ID:        "call-1",
		Name:      "echo",
		Arguments: `{"x":1}`,
	})

	if result.ID != "call-1" {
		t.Errorf("ID not propagated: %q", result.ID)
	}
	if result.IsError {
		t.Errorf("unexpected IsError=true: %+v", result)
	}
	if !strings.Contains(result.Output, "echo-output-for-") {
		t.Errorf("output not propagated: %q", result.Output)
	}
}

func TestToolingExecutorAdapter_Execute_PropagatesError(t *testing.T) {
	exec := &tooling.Executor{}
	exec.RegisterNative(fakeNativeTool{
		name: "breaks",
		run: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("kaboom")
		},
	})

	adapter := NewToolingExecutorAdapter(exec)
	result := adapter.Execute(context.Background(), ToolCallRequest{
		Name:      "breaks",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("IsError should propagate")
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (native error signature)", result.ExitCode)
	}
	if !strings.Contains(result.ErrorMessage, "kaboom") {
		t.Errorf("error message not propagated: %q", result.ErrorMessage)
	}
}

// ----- tagLastMessageCache --------------------------------------------

func TestTagLastMessageCache_TagsLastNonSystem(t *testing.T) {
	msgs := []apiMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "last"},
	}
	tagLastMessageCache(msgs)

	if msgs[3].CacheControl == nil {
		t.Fatal("last non-system message should be tagged")
	}
	if msgs[3].CacheControl.Type != "ephemeral" {
		t.Errorf("wrong cache type: %q", msgs[3].CacheControl.Type)
	}
	// Other non-system messages must not be tagged.
	if msgs[1].CacheControl != nil || msgs[2].CacheControl != nil {
		t.Errorf("earlier messages tagged: %+v %+v", msgs[1].CacheControl, msgs[2].CacheControl)
	}
}

func TestTagLastMessageCache_ClearsPreviousNonSystemTags(t *testing.T) {
	previous := &apiCacheControl{Type: "ephemeral"}
	msgs := []apiMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi", CacheControl: previous},  // old tag
		{Role: "assistant", Content: "reply"},
	}

	tagLastMessageCache(msgs)

	if msgs[1].CacheControl != nil {
		t.Error("previous non-system cache tag should be cleared")
	}
	if msgs[2].CacheControl == nil {
		t.Error("new last message should be tagged")
	}
}

func TestTagLastMessageCache_AllSystem_NoOp(t *testing.T) {
	msgs := []apiMessage{
		{Role: "system", Content: "a"},
		{Role: "system", Content: "b"},
	}

	tagLastMessageCache(msgs)

	for i, m := range msgs {
		if m.CacheControl != nil {
			t.Errorf("msg[%d] got tagged despite being system-only: %+v", i, m.CacheControl)
		}
	}
}

// ----- Registry.RegisterProvider --------------------------------------

type stubInvoker struct{ name string }

func (s *stubInvoker) Invoke(_ context.Context, _ string, _ Params, _ OnProgress) (*Result, error) {
	return &Result{Text: s.name}, nil
}

func TestRegistry_RegisterProvider_StoresGeneric(t *testing.T) {
	r := NewRegistry(&CLIProvider{})
	stub := &stubInvoker{name: "codex"}

	r.RegisterProvider("codex", stub)

	if !r.HasBackend("codex") {
		t.Error("HasBackend should be true after RegisterProvider")
	}
	names := r.BackendNames()
	found := false
	for _, n := range names {
		if n == "codex" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("BackendNames does not include registered generic: %v", names)
	}
}

// ----- LLMLogger ------------------------------------------------------

func TestInitLLMLog_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	InitLLMLog(dir)
	t.Cleanup(CloseLLMLog)

	logDir := filepath.Join(dir, "logs", "llm")
	info, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("logs/llm not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("logs/llm is not a directory")
	}
}

func TestLogLLM_WritesJSONLEntry(t *testing.T) {
	dir := t.TempDir()
	InitLLMLog(dir)
	t.Cleanup(CloseLLMLog)

	logLLM("test-event", map[string]any{"k": "v"})

	// File is named today.jsonl.
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "llm", today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file missing: %v", err)
	}

	var rec map[string]any
	line := strings.TrimSpace(string(data))
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("log line not valid JSON: %v\n%s", err, line)
	}
	if rec["event"] != "test-event" || rec["k"] != "v" {
		t.Errorf("log record missing fields: %+v", rec)
	}
	if _, ok := rec["ts"]; !ok {
		t.Error("timestamp missing")
	}
}

func TestLogLLM_NoInit_NoPanic(t *testing.T) {
	// Reset the package-level state.
	CloseLLMLog()
	llmLog = nil

	// Must not panic when logger is nil.
	logLLM("orphan", map[string]any{"x": 1})
}

func TestCloseLLMLog_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	InitLLMLog(dir)
	logLLM("first", nil) // force file open
	CloseLLMLog()
	CloseLLMLog() // second call must not panic / must not error
}

// ----- parseJSONResult -------------------------------------------------

func TestParseJSONResult_HappyPath(t *testing.T) {
	raw := `{
		"type":"result",
		"session_id":"sess-1",
		"result":"hello from claude",
		"is_error":false,
		"num_turns":3,
		"total_cost_usd":0.0042,
		"modelUsage":{"claude-sonnet":{"tokens":100}}
	}`

	result, err := parseJSONResult(context.Background(), []byte(raw), "", nil)
	if err != nil {
		t.Fatalf("parseJSONResult: %v", err)
	}
	if result.Text != "hello from claude" {
		t.Errorf("text = %q", result.Text)
	}
	if result.SessionID != "sess-1" {
		t.Errorf("session = %q", result.SessionID)
	}
	if result.Model != "claude-sonnet" {
		t.Errorf("model = %q", result.Model)
	}
	if result.NumTurns != 3 {
		t.Errorf("turns = %d", result.NumTurns)
	}
	if result.CostUSD != 0.0042 {
		t.Errorf("cost = %f", result.CostUSD)
	}
}

func TestParseJSONResult_ErrorSubtype(t *testing.T) {
	raw := `{"type":"result","is_error":true,"subtype":"rate_limited"}`

	_, err := parseJSONResult(context.Background(), []byte(raw), "", nil)
	if err == nil {
		t.Fatal("expected error on is_error=true")
	}
	if !strings.Contains(err.Error(), "rate_limited") {
		t.Errorf("error should mention subtype: %v", err)
	}
}

func TestParseJSONResult_MaxTurnsFriendlyText(t *testing.T) {
	raw := `{"type":"result","is_error":false,"subtype":"error_max_turns"}`

	result, err := parseJSONResult(context.Background(), []byte(raw), "", nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(result.Text, "Turn limit") {
		t.Errorf("expected friendly turn-limit text, got %q", result.Text)
	}
}

func TestParseJSONResult_NoConversationFound_ReturnsError(t *testing.T) {
	raw := `{"type":"result","is_error":true,"result":"No conversation found for session xxx"}`

	_, err := parseJSONResult(context.Background(), []byte(raw), "", nil)
	if err == nil {
		t.Fatal("expected error on stale session")
	}
	if !strings.Contains(err.Error(), "No conversation found") {
		t.Errorf("error text: %v", err)
	}
}

func TestParseJSONResult_RawTextFallback(t *testing.T) {
	// Non-JSON output — should fall back to returning raw text.
	result, err := parseJSONResult(context.Background(), []byte("plain text output"), "", nil)
	if err != nil {
		t.Fatalf("fallback path errored: %v", err)
	}
	if result == nil || result.Text != "plain text output" {
		t.Errorf("fallback mismatch: %+v", result)
	}
}

func TestParseJSONResult_WaitErrorBubblesWithStderr(t *testing.T) {
	_, err := parseJSONResult(context.Background(), []byte("garbage"), "out of memory", errors.New("exit 137"))
	if err == nil {
		t.Fatal("expected error when CLI exited non-zero")
	}
	if !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("stderr should be included in error: %v", err)
	}
}
