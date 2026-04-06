package tooling

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockLLMService struct {
	mu      sync.Mutex
	result  string
	err     error
	calls   []LLMInvokeOpts // records all calls
}

func (m *mockLLMService) Invoke(_ context.Context, opts LLMInvokeOpts) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, opts)
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

func (m *mockLLMService) getCalls() []LLMInvokeOpts {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]LLMInvokeOpts, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// --- Sync mode tests (unchanged behavior) ---

func TestLLMTool_Invoke(t *testing.T) {
	svc := &mockLLMService{result: "The text is about climate change."}
	tool := LLMNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"Classify this text: ..."}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "climate change") {
		t.Fatalf("expected LLM result, got: %s", out)
	}
}

func TestLLMTool_InvokeWithSystem(t *testing.T) {
	svc := &mockLLMService{result: "Résumé: ..."}
	tool := LLMNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"tier":"sonnet","prompt":"Summarize this","system":"Reply in French"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Résumé: ..." {
		t.Fatalf("expected French result, got: %s", out)
	}
}

func TestLLMTool_MissingTier(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"prompt":"hello"}`)
	if err == nil || !strings.Contains(err.Error(), "tier is required") {
		t.Fatalf("expected tier required error, got: %v", err)
	}
}

func TestLLMTool_MissingPrompt(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"tier":"haiku"}`)
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected prompt required error, got: %v", err)
	}
}

func TestLLMTool_InvokeError(t *testing.T) {
	svc := &mockLLMService{err: fmt.Errorf("tier 'bad' not found")}
	tool := LLMNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"tier":"bad","prompt":"test"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected error, got: %v", err)
	}
}

func TestLLMTool_InvalidJSON(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{bad}`)
	if err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestLLMTool_Schema(t *testing.T) {
	s := LLMNativeTool{}.Schema()
	if s.Name != "llm" {
		t.Fatalf("expected 'llm', got %q", s.Name)
	}
	props, ok := s.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties")
	}
	for _, key := range []string{"tier", "prompt", "fire_and_forget", "on_complete", "max_depth"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("expected %q property in schema", key)
		}
	}
}

// --- Fire-and-forget validation tests ---

func TestLLMTool_FireAndForget_RequiresOnComplete(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"test","fire_and_forget":true,"max_depth":2}`)
	if err == nil || !strings.Contains(err.Error(), "on_complete is required") {
		t.Fatalf("expected on_complete required error, got: %v", err)
	}
}

func TestLLMTool_FireAndForget_RequiresMaxDepth(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"test","fire_and_forget":true,"on_complete":{"tier":"sonnet","prompt":"next: {result}"}}`)
	if err == nil || !strings.Contains(err.Error(), "max_depth must be > 0") {
		t.Fatalf("expected max_depth error, got: %v", err)
	}
}

func TestLLMTool_FireAndForget_ZeroMaxDepth(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"test","fire_and_forget":true,"max_depth":0,"on_complete":{"tier":"sonnet","prompt":"next"}}`)
	if err == nil || !strings.Contains(err.Error(), "max_depth must be > 0") {
		t.Fatalf("expected max_depth error, got: %v", err)
	}
}

// --- Fire-and-forget execution tests ---

func TestLLMTool_FireAndForget_ReturnsChainID(t *testing.T) {
	svc := &mockLLMService{result: "done"}
	tool := LLMNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"test","fire_and_forget":true,"max_depth":1,"on_complete":{"tier":"sonnet","prompt":"next: {result}"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "chain_id") || !strings.Contains(out, "launched") {
		t.Fatalf("expected chain_id and launched status, got: %s", out)
	}
}

func TestLLMTool_FireAndForget_TwoStepChain(t *testing.T) {
	svc := &mockLLMService{result: "step output"}
	var notified sync.WaitGroup
	notified.Add(1)
	var notifyChainID, notifyStatus, notifyMessage string

	tool := LLMNativeTool{
		Service: svc,
		NotifyFunc: func(_ ChainOrigin, chainID, status, message string) {
			notifyChainID = chainID
			notifyStatus = status
			notifyMessage = message
			notified.Done()
		},
	}

	_, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"step1","fire_and_forget":true,"max_depth":2,"on_complete":{"tier":"sonnet","prompt":"process: {result}"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for chain to complete.
	done := make(chan struct{})
	go func() { notified.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("chain did not complete in time")
	}

	if notifyChainID == "" {
		t.Fatal("expected chain ID in notification")
	}
	if notifyStatus != "completed" {
		t.Fatalf("expected completed, got %s", notifyStatus)
	}
	if notifyMessage != "step output" {
		t.Fatalf("expected 'step output', got %q", notifyMessage)
	}

	// Verify both tiers were called.
	calls := svc.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Tier != "haiku" {
		t.Fatalf("expected first call to haiku, got %s", calls[0].Tier)
	}
	if calls[1].Tier != "sonnet" {
		t.Fatalf("expected second call to sonnet, got %s", calls[1].Tier)
	}
	// Verify {result} was replaced with chain_result block.
	if !strings.Contains(calls[1].Prompt, "<chain_result status=\"200\">") {
		t.Fatalf("expected chain_result in callback prompt, got: %s", calls[1].Prompt)
	}
	if !strings.Contains(calls[1].Prompt, "step output") {
		t.Fatalf("expected step output in callback prompt, got: %s", calls[1].Prompt)
	}
}

func TestLLMTool_FireAndForget_ThreeStepChain(t *testing.T) {
	svc := &mockLLMService{result: "output"}
	var notified sync.WaitGroup
	notified.Add(1)

	tool := LLMNativeTool{
		Service: svc,
		NotifyFunc: func(_ ChainOrigin, _, _, _ string) {
			notified.Done()
		},
	}

	_, err := tool.Run(context.Background(), `{
		"tier":"haiku","prompt":"step1",
		"fire_and_forget":true,"max_depth":3,
		"on_complete":{
			"tier":"sonnet","prompt":"step2: {result}",
			"fire_and_forget":true,
			"on_complete":{"tier":"haiku","prompt":"step3: {result}"}
		}
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	done := make(chan struct{})
	go func() { notified.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("chain did not complete in time")
	}

	calls := svc.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0].Tier != "haiku" || calls[1].Tier != "sonnet" || calls[2].Tier != "haiku" {
		t.Fatalf("unexpected tier sequence: %s, %s, %s", calls[0].Tier, calls[1].Tier, calls[2].Tier)
	}
}

func TestLLMTool_FireAndForget_ErrorPropagation(t *testing.T) {
	svc := &mockLLMService{err: fmt.Errorf("tier 'bad' not found")}
	var notified sync.WaitGroup
	notified.Add(1)
	tool := LLMNativeTool{
		Service: svc,
		NotifyFunc: func(_ ChainOrigin, _, _, _ string) {
			notified.Done()
		},
	}

	_, err := tool.Run(context.Background(), `{"tier":"bad","prompt":"test","fire_and_forget":true,"max_depth":2,"on_complete":{"tier":"sonnet","prompt":"handle: {result}"}}`)
	if err != nil {
		t.Fatalf("unexpected error on dispatch: %v", err)
	}

	done := make(chan struct{})
	go func() { notified.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("chain did not complete in time")
	}

	calls := svc.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(calls))
	}
	// Second call should have the error injected.
	if !strings.Contains(calls[1].Prompt, "<chain_result status=\"404\">") {
		t.Fatalf("expected 404 status in callback, got: %s", calls[1].Prompt)
	}
}

func TestLLMTool_FireAndForget_MaxDepthExhausted(t *testing.T) {
	svc := &mockLLMService{result: "output"}
	var notified sync.WaitGroup
	notified.Add(1)
	var notifyMessage string

	tool := LLMNativeTool{
		Service: svc,
		NotifyFunc: func(_ ChainOrigin, _, _, message string) {
			notifyMessage = message
			notified.Done()
		},
	}

	// max_depth=1 but chain has 2 steps → should stop after first and notify.
	_, err := tool.Run(context.Background(), `{
		"tier":"haiku","prompt":"step1",
		"fire_and_forget":true,"max_depth":1,
		"on_complete":{
			"tier":"sonnet","prompt":"step2: {result}",
			"fire_and_forget":true,
			"on_complete":{"tier":"haiku","prompt":"step3: {result}"}
		}
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	done := make(chan struct{})
	go func() { notified.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("chain did not complete in time")
	}

	// Only step 1 runs; depth exhausted before on_complete can execute.
	calls := svc.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (step1 only, depth exhausted), got %d", len(calls))
	}
	if notifyMessage != "output" {
		t.Fatalf("expected 'output' in notify, got %q", notifyMessage)
	}
}

// --- Helper function tests ---

func TestInjectChainResult(t *testing.T) {
	result := LLMChainResult{Status: 200, Message: "hello world"}
	prompt := "Process this: {result}"
	got := InjectChainResult(prompt, result)
	expected := "Process this: <chain_result status=\"200\">\nhello world\n</chain_result>"
	if got != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestInjectChainResult_ErrorStatus(t *testing.T) {
	result := LLMChainResult{Status: 500, Message: "internal error"}
	prompt := "Handle: {result}"
	got := InjectChainResult(prompt, result)
	if !strings.Contains(got, `status="500"`) {
		t.Fatalf("expected status 500, got: %s", got)
	}
}

func TestInjectChainResult_NoPlaceholder(t *testing.T) {
	result := LLMChainResult{Status: 200, Message: "data"}
	prompt := "No placeholder here"
	got := InjectChainResult(prompt, result)
	if got != prompt {
		t.Fatalf("expected unchanged prompt, got: %s", got)
	}
}

func TestErrorToChainResult(t *testing.T) {
	tests := []struct {
		err    string
		status int
	}{
		{"tier 'x' not found", 404},
		{"context deadline exceeded", 408},
		{"timeout after 30s", 408},
		{"invalid arguments", 400},
		{"something went wrong", 500},
	}
	for _, tt := range tests {
		r := ErrorToChainResult(fmt.Errorf("%s", tt.err))
		if r.Status != tt.status {
			t.Errorf("error %q: expected status %d, got %d", tt.err, tt.status, r.Status)
		}
	}
}
