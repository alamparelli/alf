package comms

import (
	"context"
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/ai"
	provider "github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/runtime"
)

// fakeRuntime scripts a single ConverseStream call so invokeViaRuntime can be
// exercised in isolation. Only ConverseStream is exercised; the other Runtime
// methods panic if called (fail-fast on surface misuse).
type fakeRuntime struct {
	events []runtime.Event
	err    error
	gotReq runtime.ConverseRequest
}

func (f *fakeRuntime) ConverseStream(_ context.Context, req runtime.ConverseRequest) (<-chan runtime.Event, error) {
	f.gotReq = req
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan runtime.Event, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (f *fakeRuntime) Chat(context.Context, runtime.ChatRequest) (<-chan runtime.Event, error) {
	panic("Chat not expected")
}

func (f *fakeRuntime) Invoke(context.Context, capability.ID, runtime.Args) (runtime.Output, error) {
	panic("Invoke not expected")
}

func (f *fakeRuntime) Converse(context.Context, runtime.ConverseRequest) (runtime.ConverseResult, error) {
	panic("Converse not expected")
}

// TestInvokeViaRuntime_TranslatesEventsToStreamEvents (#340 R4j3) pins the
// parity contract: each runtime.Event variant round-trips through the
// progress callback as the matching provider.StreamEvent. This is the
// invariant that keeps the Accumulator + OutEvent emissions byte-identical
// to the legacy prov.Invoke path.
func TestInvokeViaRuntime_TranslatesEventsToStreamEvents(t *testing.T) {
	rt := &fakeRuntime{events: []runtime.Event{
		{Kind: runtime.EventThinking, Text: "pondering"},
		{Kind: runtime.EventToolUse, ToolName: "grep"},
		{Kind: runtime.EventToolInput, ToolName: "grep", Text: `{"p":"x"}`},
		{Kind: runtime.EventToolOutput, ToolID: "call_1", Text: "match\n"},
		{Kind: runtime.EventToken, Token: "done"},
		{Kind: runtime.EventDone, Usage: &ai.Usage{InputTokens: 12, OutputTokens: 5, CostUSD: 0.001, Model: "m", NumTurns: 2, SessionID: "sid-42"}},
	}}
	e := &ChatEngine{Runtime: rt}

	var captured []provider.StreamEvent
	progressFn := func(ev provider.StreamEvent) { captured = append(captured, ev) }

	result, err := e.invokeViaRuntime(context.Background(), runtime.ConverseRequest{Prompt: "hi"}, progressFn)
	if err != nil {
		t.Fatalf("invokeViaRuntime: %v", err)
	}
	// Translation parity.
	want := []provider.StreamEvent{
		{Type: "thinking", Text: "pondering"},
		{Type: "tool_use", Detail: "grep"},
		{Type: "tool_input", Detail: "grep", Text: `{"p":"x"}`},
		{Type: "tool_result", Detail: "call_1", Text: "match\n"},
		{Type: "text_delta", Text: "done"},
	}
	if len(captured) != len(want) {
		t.Fatalf("event count: got %d want %d (%+v)", len(captured), len(want), captured)
	}
	for i := range want {
		if captured[i] != want[i] {
			t.Fatalf("event[%d]: got %+v want %+v", i, captured[i], want[i])
		}
	}
	// Result materialised from Usage + token accumulation.
	if result.Text != "done" {
		t.Fatalf("Text: got %q want done", result.Text)
	}
	if result.Model != "m" || result.InputTokens != 12 || result.OutputTokens != 5 || result.CostUSD != 0.001 || result.SessionID != "sid-42" || result.NumTurns != 2 {
		t.Fatalf("Result fields not materialised from Usage: %+v", result)
	}
}

// TestInvokeViaRuntime_SurfacesStreamError pins the error contract: a
// mid-stream runtime.EventError is returned verbatim to the caller so
// processStandard's retry + fallback path can react as if prov.Invoke had
// failed.
func TestInvokeViaRuntime_SurfacesStreamError(t *testing.T) {
	boom := errors.New("provider blew up")
	rt := &fakeRuntime{events: []runtime.Event{
		{Kind: runtime.EventToken, Token: "partial"},
		{Kind: runtime.EventError, Err: boom},
	}}
	e := &ChatEngine{Runtime: rt}

	_, err := e.invokeViaRuntime(context.Background(), runtime.ConverseRequest{Prompt: "hi"}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("error: got %v want wrapping %v", err, boom)
	}
}

// TestInvokeViaRuntime_NilRuntimeErrors pins the guard: if somebody calls the
// helper while Runtime is still nil (early boot), we fail fast with a clear
// message rather than NPE.
func TestInvokeViaRuntime_NilRuntimeErrors(t *testing.T) {
	e := &ChatEngine{}
	_, err := e.invokeViaRuntime(context.Background(), runtime.ConverseRequest{Prompt: "hi"}, nil)
	if err == nil {
		t.Fatal("expected error when Runtime is nil")
	}
}

// TestBuildConverseRequest_PreservesAllPassthroughs (#340 R4j3) locks in that
// the helper copies every provider.Params field the pipeline sets today onto
// the ConverseRequest — missing one is a silent behaviour drift for the
// migrated processStandard path.
func TestBuildConverseRequest_PreservesAllPassthroughs(t *testing.T) {
	prov := &stubNoopProvider{}
	params := provider.Params{
		Model:           "m",
		Tools:           []string{"grep", "", "read"}, // empty names dropped
		WriteCapable:    true,
		Effort:          "high",
		SystemPrompts:   []string{"sys-a", "sys-b"},
		CacheBreakpoint: 2,
		MaxTurns:        7,
		ResumeID:        "sess-xyz",
		DataDir:         "/data",
		Env:             []string{"FOO=bar"},
		ConvMessages: []provider.ContextMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		Media: []provider.MediaEntry{{Type: "photo", FileName: "a.png", MimeType: "image/png", TempPath: "/tmp/a"}},
	}
	req := buildConverseRequest("final-prompt", prov, params)

	if req.Prompt != "final-prompt" {
		t.Fatalf("Prompt: got %q", req.Prompt)
	}
	if string(req.Model) != "m" || req.Effort != "high" || !req.WriteCapable || req.MaxTurns != 7 || req.DataDir != "/data" || req.CacheBreakpoint != 2 || req.ResumeID != "sess-xyz" {
		t.Fatalf("scalar passthroughs drift: %+v", req)
	}
	if len(req.SystemPrompts) != 2 || req.SystemPrompts[0] != "sys-a" {
		t.Fatalf("SystemPrompts: %+v", req.SystemPrompts)
	}
	if len(req.Tools) != 2 || req.Tools[0].Name != "grep" || req.Tools[1].Name != "read" {
		t.Fatalf("Tools: %+v", req.Tools)
	}
	if len(req.History) != 2 || req.History[0].Role != ai.RoleUser || req.History[1].Role != ai.RoleAssistant {
		t.Fatalf("History: %+v", req.History)
	}
	if len(req.Media) != 1 || req.Media[0].FileName != "a.png" {
		t.Fatalf("Media: %+v", req.Media)
	}
	if len(req.Env) != 1 || req.Env[0] != "FOO=bar" {
		t.Fatalf("Env: %+v", req.Env)
	}
	if req.Engine == nil {
		t.Fatal("Engine override must be populated so tool-loop wrapping survives the hop")
	}
}

type stubNoopProvider struct{}

func (stubNoopProvider) Invoke(context.Context, string, provider.Params, provider.OnProgress) (*provider.Result, error) {
	return &provider.Result{}, nil
}
