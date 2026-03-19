package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNewCodexProvider_DefaultTimeout(t *testing.T) {
	p := NewCodexProvider("/tmp", 0, "sk-test")
	if p.Timeout != 5*time.Minute {
		t.Errorf("expected default timeout 5m, got %v", p.Timeout)
	}
}

func TestNewCodexProvider_CustomTimeout(t *testing.T) {
	p := NewCodexProvider("/tmp", 2*time.Minute, "sk-test")
	if p.Timeout != 2*time.Minute {
		t.Errorf("expected timeout 2m, got %v", p.Timeout)
	}
}

func TestCodexProvider_InvokeNoCodex(t *testing.T) {
	dir := t.TempDir()
	p := NewCodexProvider(dir, 5*time.Second, "sk-test")

	t.Setenv("PATH", t.TempDir())

	_, err := p.Invoke(context.Background(), "hello", Params{}, nil)
	if err == nil {
		t.Fatal("expected error when codex binary not found")
	}
}

func TestCodexProvider_InvokeCancelled(t *testing.T) {
	dir := t.TempDir()
	p := NewCodexProvider(dir, 5*time.Second, "sk-test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Invoke(ctx, "hello", Params{}, nil)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestCodexEvent_ParseThreadStarted(t *testing.T) {
	raw := `{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"}`
	var evt codexEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Type != "thread.started" {
		t.Errorf("expected thread.started, got %s", evt.Type)
	}
	if evt.ThreadID != "0199a213-81c0-7800-8aa1-bbab2a035a53" {
		t.Errorf("unexpected thread_id: %s", evt.ThreadID)
	}
}

func TestCodexEvent_ParseItemCompleted(t *testing.T) {
	raw := `{"type":"item.completed","item":{"id":"item_3","type":"agent_message","text":"Hello world"}}`
	var evt codexEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Type != "item.completed" {
		t.Errorf("expected item.completed, got %s", evt.Type)
	}
	if evt.Item.Type != "agent_message" {
		t.Errorf("expected agent_message, got %s", evt.Item.Type)
	}
	if evt.Item.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", evt.Item.Text)
	}
}

func TestCodexEvent_ParseCommandExecution(t *testing.T) {
	raw := `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"bash -lc ls"}}`
	var evt codexEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Item.Command != "bash -lc ls" {
		t.Errorf("expected 'bash -lc ls', got %q", evt.Item.Command)
	}
}

func TestCodexEvent_ParseTurnCompleted(t *testing.T) {
	raw := `{"type":"turn.completed","usage":{"input_tokens":24763,"cached_input_tokens":24448,"output_tokens":122}}`
	var evt codexEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Usage.InputTokens != 24763 {
		t.Errorf("expected 24763 input tokens, got %d", evt.Usage.InputTokens)
	}
	if evt.Usage.OutputTokens != 122 {
		t.Errorf("expected 122 output tokens, got %d", evt.Usage.OutputTokens)
	}
	if evt.Usage.CachedInputTokens != 24448 {
		t.Errorf("expected 24448 cached tokens, got %d", evt.Usage.CachedInputTokens)
	}
}

func TestCodexEvent_ParseError(t *testing.T) {
	raw := `{"type":"error","message":"rate limit exceeded"}`
	var evt codexEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Type != "error" {
		t.Errorf("expected error, got %s", evt.Type)
	}
	if evt.Message != "rate limit exceeded" {
		t.Errorf("expected 'rate limit exceeded', got %q", evt.Message)
	}
}

func TestCodexEnv_ContainsAPIKey(t *testing.T) {
	env := codexEnv("sk-test-key-123")
	found := false
	for _, e := range env {
		if e == "CODEX_API_KEY=sk-test-key-123" {
			found = true
			break
		}
	}
	if !found {
		t.Error("CODEX_API_KEY not found in env")
	}
}

func TestCodexEnv_ContainsPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	env := codexEnv("sk-test")
	found := false
	for _, e := range env {
		if e == "PATH=/usr/bin" {
			found = true
			break
		}
	}
	if !found {
		t.Error("PATH not found in env")
	}
}
