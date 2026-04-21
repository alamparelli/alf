package provider_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/ai/provider"
)

// stubProvider is a test double that records Invoke calls and can emit
// stream events before returning a configured Result/error.
type stubProvider struct {
	mu            sync.Mutex
	streamEvents  []provider.StreamEvent
	result        *provider.Result
	err           error
	lastPrompt    string
	lastParams    provider.Params
	calls         int
	invokeCh      chan struct{} // closed once Invoke has been called
}

func (s *stubProvider) Invoke(ctx context.Context, prompt string, params provider.Params, onProgress provider.OnProgress) (*provider.Result, error) {
	s.mu.Lock()
	s.calls++
	s.lastPrompt = prompt
	s.lastParams = params
	if s.invokeCh != nil {
		close(s.invokeCh)
		s.invokeCh = nil
	}
	events := append([]provider.StreamEvent(nil), s.streamEvents...)
	s.mu.Unlock()

	if onProgress != nil {
		for _, ev := range events {
			onProgress(ev)
		}
	}
	return s.result, s.err
}

// drainEvents collects every ai.Event from ch into slices keyed by Kind.
type drained struct {
	tokens    []string
	thinking  []string
	toolUses  []string       // tool names
	toolInput []ai.Event     // preserves ToolName+Text pair
	toolOut   []ai.Event     // preserves ToolID+Text pair
	sawDone   bool
	err       error
}

func drainEvents(t *testing.T, ch <-chan ai.Event) drained {
	t.Helper()
	var d drained
	for ev := range ch {
		switch ev.Kind {
		case ai.EventToken:
			d.tokens = append(d.tokens, ev.Token)
		case ai.EventThinking:
			d.thinking = append(d.thinking, ev.Text)
		case ai.EventToolUse:
			d.toolUses = append(d.toolUses, ev.ToolName)
		case ai.EventToolInput:
			d.toolInput = append(d.toolInput, ev)
		case ai.EventToolOutput:
			d.toolOut = append(d.toolOut, ev)
		case ai.EventDone:
			d.sawDone = true
		case ai.EventError:
			d.err = ev.Err
		}
	}
	return d
}

func joinTokens(parts []string) string {
	out := ""
	for _, p := range parts {
		out += p
	}
	return out
}

// ── validation ──────────────────────────────────────────────────────────────

func TestNewEngine_NilProviderErrors(t *testing.T) {
	eng := provider.NewEngine(nil)
	_, err := eng.Run(context.Background(), ai.Request{Model: "m", Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
}

func TestRun_RejectsMissingModel(t *testing.T) {
	eng := provider.NewEngine(&stubProvider{})
	_, err := eng.Run(context.Background(), ai.Request{Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error when Request.Model empty")
	}
}

func TestRun_RejectsEmptyMessages(t *testing.T) {
	eng := provider.NewEngine(&stubProvider{})
	if _, err := eng.Run(context.Background(), ai.Request{Model: "m"}); err == nil {
		t.Fatal("expected error when Messages is empty")
	}
}

func TestRun_RejectsNoUserMessage(t *testing.T) {
	eng := provider.NewEngine(&stubProvider{})
	_, err := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleSystem, Content: "sys"}},
	})
	if err == nil {
		t.Fatal("expected error when no user message present")
	}
}

// ── happy path ──────────────────────────────────────────────────────────────

func TestRun_StreamsTextDeltasThenDone(t *testing.T) {
	stub := &stubProvider{
		streamEvents: []provider.StreamEvent{
			{Type: "thinking", Text: "reasoning"},
			{Type: "text_delta", Text: "hel"},
			{Type: "tool_use", Detail: "grep"},
			{Type: "text_delta", Text: "lo"},
		},
		result: &provider.Result{Text: "hello"},
	}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model: "test-model",
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: "sys"},
			{Role: ai.RoleUser, Content: "say hello"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	d := drainEvents(t, ch)

	if d.err != nil {
		t.Fatalf("unexpected error event: %v", d.err)
	}
	if !d.sawDone {
		t.Fatal("missing EventDone")
	}
	if joinTokens(d.tokens) != "hello" {
		t.Fatalf("tokens: got %q, want %q", joinTokens(d.tokens), "hello")
	}
	if len(d.tokens) != 2 {
		t.Fatalf("token count: got %d want 2 (no trailing duplicate)", len(d.tokens))
	}

	// The system message must land in SystemPrompts, not ConvMessages.
	if got := stub.lastParams.SystemPrompts; len(got) != 1 || got[0] != "sys" {
		t.Fatalf("SystemPrompts: got %+v, want [sys]", got)
	}
	if len(stub.lastParams.ConvMessages) != 0 {
		t.Fatalf("ConvMessages should be empty when only a system + user msg: got %+v", stub.lastParams.ConvMessages)
	}
}

// #340 R4j1: non-text_delta StreamEvents (thinking / tool_use / tool_input /
// tool_result) are forwarded to consumers as observability ai.Events so the
// pipeline can render progress without reaching into the Provider layer.
func TestRun_ForwardsProviderSubEvents(t *testing.T) {
	stub := &stubProvider{
		streamEvents: []provider.StreamEvent{
			{Type: "thinking", Text: "pondering"},
			{Type: "tool_use", Detail: "grep"},
			{Type: "tool_input", Detail: "grep", Text: `{"pattern":`},
			{Type: "tool_result", Detail: "call_abc", Text: "match\n"},
			{Type: "block_stop"}, // still dropped
			{Type: "text_delta", Text: "done"},
		},
		result: &provider.Result{Text: "done"},
	}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	d := drainEvents(t, ch)

	if d.err != nil {
		t.Fatalf("unexpected error: %v", d.err)
	}
	if len(d.thinking) != 1 || d.thinking[0] != "pondering" {
		t.Fatalf("thinking: got %+v, want [pondering]", d.thinking)
	}
	if len(d.toolUses) != 1 || d.toolUses[0] != "grep" {
		t.Fatalf("toolUses: got %+v, want [grep]", d.toolUses)
	}
	if len(d.toolInput) != 1 || d.toolInput[0].ToolName != "grep" || d.toolInput[0].Text != `{"pattern":` {
		t.Fatalf("toolInput: got %+v", d.toolInput)
	}
	if len(d.toolOut) != 1 || d.toolOut[0].ToolID != "call_abc" || d.toolOut[0].Text != "match\n" {
		t.Fatalf("toolOut: got %+v", d.toolOut)
	}
	if !d.sawDone {
		t.Fatal("missing EventDone")
	}
}

// If no OnProgress deltas arrive, the adapter must emit Result.Text as a
// single trailing token so consumers always see the full response.
func TestRun_EmitsFullResultWhenNoDeltas(t *testing.T) {
	stub := &stubProvider{
		result: &provider.Result{Text: "full response"},
	}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	d := drainEvents(t, ch)

	if d.err != nil {
		t.Fatalf("unexpected error: %v", d.err)
	}
	if !d.sawDone {
		t.Fatal("missing EventDone")
	}
	if len(d.tokens) != 1 || d.tokens[0] != "full response" {
		t.Fatalf("tokens: got %+v", d.tokens)
	}
}

// When deltas stream a prefix and Result.Text extends beyond it, only the
// suffix should be emitted at the end — no double-send.
func TestRun_EmitsOnlyMissingSuffix(t *testing.T) {
	stub := &stubProvider{
		streamEvents: []provider.StreamEvent{
			{Type: "text_delta", Text: "partial "},
		},
		result: &provider.Result{Text: "partial then full"},
	}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	d := drainEvents(t, ch)

	if d.err != nil {
		t.Fatalf("unexpected error: %v", d.err)
	}
	if joinTokens(d.tokens) != "partial then full" {
		t.Fatalf("concat tokens: got %q", joinTokens(d.tokens))
	}
}

// ── request translation ─────────────────────────────────────────────────────

func TestRun_PromptIsLastUserMessage(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model: "m",
		Messages: []ai.Message{
			{Role: ai.RoleUser, Content: "first"},
			{Role: ai.RoleAssistant, Content: "ack"},
			{Role: ai.RoleUser, Content: "latest"},
		},
	})
	drainEvents(t, ch)

	if stub.lastPrompt != "latest" {
		t.Fatalf("lastPrompt: got %q want %q", stub.lastPrompt, "latest")
	}
	if len(stub.lastParams.ConvMessages) != 2 {
		t.Fatalf("ConvMessages len: got %d want 2", len(stub.lastParams.ConvMessages))
	}
	if stub.lastParams.ConvMessages[0].Role != "user" || stub.lastParams.ConvMessages[0].Content != "first" {
		t.Fatalf("history[0] wrong: %+v", stub.lastParams.ConvMessages[0])
	}
	if stub.lastParams.ConvMessages[1].Role != "assistant" || stub.lastParams.ConvMessages[1].Content != "ack" {
		t.Fatalf("history[1] wrong: %+v", stub.lastParams.ConvMessages[1])
	}
}

// Multiple system messages must land in SystemPrompts, in order, and must
// NOT leak into ConvMessages.
func TestRun_SystemMessagesRouteToSystemPrompts(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model: "m",
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: "persona"},
			{Role: ai.RoleUser, Content: "first"},
			{Role: ai.RoleAssistant, Content: "ack"},
			{Role: ai.RoleSystem, Content: "reminder"},
			{Role: ai.RoleUser, Content: "latest"},
		},
	})
	drainEvents(t, ch)

	if got := stub.lastParams.SystemPrompts; len(got) != 2 || got[0] != "persona" || got[1] != "reminder" {
		t.Fatalf("SystemPrompts: got %+v, want [persona reminder]", got)
	}
	for _, cm := range stub.lastParams.ConvMessages {
		if cm.Role == "system" {
			t.Fatalf("system role leaked into ConvMessages: %+v", cm)
		}
	}
	if len(stub.lastParams.ConvMessages) != 2 {
		t.Fatalf("ConvMessages len: got %d want 2 (first user + assistant ack)", len(stub.lastParams.ConvMessages))
	}
}

// Empty system message content must be skipped — no blank entries in SystemPrompts.
func TestRun_EmptySystemMessageIsSkipped(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model: "m",
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: ""},
			{Role: ai.RoleSystem, Content: "non-empty"},
			{Role: ai.RoleUser, Content: "hi"},
		},
	})
	drainEvents(t, ch)

	if got := stub.lastParams.SystemPrompts; len(got) != 1 || got[0] != "non-empty" {
		t.Fatalf("SystemPrompts: got %+v, want [non-empty]", got)
	}
}

func TestRun_ModelAndToolsPropagate(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model: "opus-42",
		Tools: []ai.ToolSpec{
			{Name: "bash"},
			{Name: "read_file"},
			{Name: ""}, // dropped
		},
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	drainEvents(t, ch)

	if stub.lastParams.Model != "opus-42" {
		t.Fatalf("model: got %q want %q", stub.lastParams.Model, "opus-42")
	}
	if got := stub.lastParams.Tools; len(got) != 2 || got[0] != "bash" || got[1] != "read_file" {
		t.Fatalf("Tools: got %+v", got)
	}
}

// TestRun_ResumeIDPropagates locks the #340 R4e passthrough so a caller using
// Runtime.Converse can continue a provider-side session (Claude CLI resume).
func TestRun_ResumeIDPropagates(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		ResumeID: "sess-abc",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "continue"}},
	})
	drainEvents(t, ch)

	if stub.lastParams.ResumeID != "sess-abc" {
		t.Fatalf("ResumeID: got %q want %q", stub.lastParams.ResumeID, "sess-abc")
	}
}

// ── errors ──────────────────────────────────────────────────────────────────

func TestRun_ProviderError_SurfacesEventError(t *testing.T) {
	boom := errors.New("provider failure")
	stub := &stubProvider{err: boom}
	eng := provider.NewEngine(stub)

	ch, _ := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	d := drainEvents(t, ch)

	if d.sawDone {
		t.Fatal("should not emit EventDone when Invoke errors")
	}
	if d.err == nil || !errors.Is(d.err, boom) {
		t.Fatalf("error event: got %v want wrap of %v", d.err, boom)
	}
}

// ── cancellation ────────────────────────────────────────────────────────────

func TestRun_ContextCancelPropagatesAsError(t *testing.T) {
	stub := &stubProvider{
		err: context.Canceled,
	}
	eng := provider.NewEngine(stub)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, _ := eng.Run(ctx, ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})

	// Drain with a safety timeout so a bugged adapter doesn't hang CI.
	timeout := time.After(500 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-timeout:
		t.Fatal("adapter did not close channel after ctx cancel")
	}
}

// ── #340 R5b: SystemPrompts per-call + Usage on EventDone ───────────────────

// TestRun_RequestSystemPromptsForwarded pins that ai.Request.SystemPrompts
// lands in Params.SystemPrompts before any RoleSystem message from Messages.
func TestRun_RequestSystemPromptsForwarded(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model:         "m",
		SystemPrompts: []string{"identity", "job-context"},
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: "from-history"},
			{Role: ai.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	got := stub.lastParams.SystemPrompts
	want := []string{"identity", "job-context", "from-history"}
	if len(got) != len(want) {
		t.Fatalf("SystemPrompts len: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SystemPrompts[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

// TestRun_RequestSystemPromptsDropsEmpty verifies the merge helper silently
// discards empty strings from either source rather than propagating them to
// the Provider (empty system prompts upset some backends and waste tokens).
func TestRun_RequestSystemPromptsDropsEmpty(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model:         "m",
		SystemPrompts: []string{"", "keep"},
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: ""},
			{Role: ai.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	got := stub.lastParams.SystemPrompts
	if len(got) != 1 || got[0] != "keep" {
		t.Fatalf("SystemPrompts after drop: got %v want [keep]", got)
	}
}

// TestRun_EventDoneCarriesUsage proves the Provider.Result metadata (cost,
// model, turns, session) is surfaced as ai.Usage on the terminal EventDone.
func TestRun_EventDoneCarriesUsage(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{
		Text:      "hello",
		Model:     "actual-model",
		CostUSD:   0.0123,
		NumTurns:  4,
		SessionID: "sess-42",
	}}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var done ai.Event
	for ev := range ch {
		if ev.Kind == ai.EventDone {
			done = ev
		}
	}
	if done.Usage == nil {
		t.Fatal("EventDone.Usage is nil — provider Result was non-nil")
	}
	if done.Usage.Model != "actual-model" {
		t.Fatalf("Usage.Model: got %q want %q", done.Usage.Model, "actual-model")
	}
	if done.Usage.CostUSD != 0.0123 {
		t.Fatalf("Usage.CostUSD: got %v want 0.0123", done.Usage.CostUSD)
	}
	if done.Usage.NumTurns != 4 {
		t.Fatalf("Usage.NumTurns: got %d want 4", done.Usage.NumTurns)
	}
	if done.Usage.SessionID != "sess-42" {
		t.Fatalf("Usage.SessionID: got %q want %q", done.Usage.SessionID, "sess-42")
	}
}

// TestRun_RequestPassesProviderFields pins the #340 R5d passthrough: tier-
// level Effort / WriteCapable / MaxTurns / DataDir flow from ai.Request to
// Params without adapter-side filtering. Previously (pre-R5d) these were
// silently dropped; the scheduler migration relies on them reaching the
// provider intact.
func TestRun_RequestPassesProviderFields(t *testing.T) {
	stub := &stubProvider{result: &provider.Result{Text: "ok"}}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model:        "m",
		Effort:       "high",
		WriteCapable: true,
		MaxTurns:     12,
		DataDir:      "/var/alf",
		Messages:     []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	if got := stub.lastParams.Effort; got != "high" {
		t.Fatalf("Params.Effort: got %q want high", got)
	}
	if !stub.lastParams.WriteCapable {
		t.Fatal("Params.WriteCapable: got false want true")
	}
	if got := stub.lastParams.MaxTurns; got != 12 {
		t.Fatalf("Params.MaxTurns: got %d want 12", got)
	}
	if got := stub.lastParams.DataDir; got != "/var/alf" {
		t.Fatalf("Params.DataDir: got %q want /var/alf", got)
	}
}

// TestRun_EventDoneUsageNilWhenNoResult covers the edge case: Provider
// returned (nil, nil). EventDone still fires so the Runtime can finalise,
// but Usage is absent.
func TestRun_EventDoneUsageNilWhenNoResult(t *testing.T) {
	stub := &stubProvider{result: nil}
	eng := provider.NewEngine(stub)

	ch, err := eng.Run(context.Background(), ai.Request{
		Model:    "m",
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var done ai.Event
	for ev := range ch {
		if ev.Kind == ai.EventDone {
			done = ev
		}
	}
	if done.Kind != ai.EventDone {
		t.Fatal("missing EventDone")
	}
	if done.Usage != nil {
		t.Fatalf("Usage should be nil when result is nil, got %+v", done.Usage)
	}
}
