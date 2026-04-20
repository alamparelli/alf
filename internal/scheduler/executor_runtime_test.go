package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/runtime"
)

// fakeRuntimeInvoker captures the last Invoke/Converse call and returns a
// scripted Output/error. Narrow enough to satisfy scheduler.RuntimeInvoker
// without building a real runtime.Runtime.
type fakeRuntimeInvoker struct {
	calls         []fakeRuntimeCall
	out           capability.Output
	err           error
	converseCalls []runtime.ConverseRequest
	converseOut   runtime.ConverseResult
	converseErr   error
}

type fakeRuntimeCall struct {
	capID capability.ID
	args  runtime.Args
}

func (f *fakeRuntimeInvoker) Invoke(_ context.Context, capID capability.ID, args runtime.Args) (capability.Output, error) {
	f.calls = append(f.calls, fakeRuntimeCall{capID: capID, args: args})
	return f.out, f.err
}

func (f *fakeRuntimeInvoker) Converse(_ context.Context, req runtime.ConverseRequest) (runtime.ConverseResult, error) {
	f.converseCalls = append(f.converseCalls, req)
	return f.converseOut, f.converseErr
}

// TestInvokeDirectCommand_RoutesThroughRuntime asserts that when Config.Runtime
// is set, invokeDirectCommand calls Runtime.Invoke(CommandCapabilityID, args)
// instead of running bash inline. This is the surface migration #340 R5a pins.
func TestInvokeDirectCommand_RoutesThroughRuntime(t *testing.T) {
	rt := &fakeRuntimeInvoker{out: capability.Output{Data: "hello"}}
	e := &Engine{cfg: Config{Runtime: rt}}
	j := &Job{ID: "j1", Command: "echo hello", Timeout: 3 * time.Second}

	text, err := e.invokeDirectCommand(j)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello" {
		t.Fatalf("text: got %q want %q", text, "hello")
	}
	if len(rt.calls) != 1 {
		t.Fatalf("Invoke call count: got %d want 1", len(rt.calls))
	}
	call := rt.calls[0]
	if call.capID != CommandCapabilityID {
		t.Fatalf("capID: got %q want %q", call.capID, CommandCapabilityID)
	}
	if cmd, _ := call.args["command"].(string); cmd != "echo hello" {
		t.Fatalf("args[command]: got %v want %q", call.args["command"], "echo hello")
	}
	if to, _ := call.args["timeout"].(time.Duration); to != 3*time.Second {
		t.Fatalf("args[timeout]: got %v want 3s", call.args["timeout"])
	}
}

// TestInvokeDirectCommand_NoRuntimeFallsBackToInline proves back-compat: when
// Runtime is not configured (existing deployments), invokeDirectCommand calls
// the legacy runCommand path and executes bash directly.
func TestInvokeDirectCommand_NoRuntimeFallsBackToInline(t *testing.T) {
	e := &Engine{cfg: Config{}}
	j := &Job{ID: "j-legacy", Command: "echo legacy", Timeout: 3 * time.Second}
	text, err := e.invokeDirectCommand(j)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "legacy" {
		t.Fatalf("text: got %q want %q", text, "legacy")
	}
}

// TestInvokeDirectCommand_RuntimeErrorSurfaces makes sure an Invoke-level error
// is propagated as-is — the scheduler does NOT silently fall back to inline
// execution when Runtime is configured (hiding bugs would be worse than
// failing visibly).
func TestInvokeDirectCommand_RuntimeErrorSurfaces(t *testing.T) {
	rt := &fakeRuntimeInvoker{err: errors.New("boom")}
	e := &Engine{cfg: Config{Runtime: rt}}
	j := &Job{ID: "j-err", Command: "echo x"}

	_, err := e.invokeDirectCommand(j)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected 'boom' error, got %v", err)
	}
}

// TestInvokeLLMViaRuntime_PassesTierAndJobContext walks the new LLM path:
// the tier's Backend/Model/Tools/Effort/WriteCapable/MaxTurns are read from
// the tier store and placed on ConverseRequest; job context + L1/L2/L3
// prompts + skill block are stacked on SystemPrompts in the same order as
// the legacy path; Usage flows back into execResult.
func TestInvokeLLMViaRuntime_PassesTierAndJobContext(t *testing.T) {
	rt := &fakeRuntimeInvoker{
		converseOut: runtime.ConverseResult{Text: "llm said hi"},
	}
	// Fake tier store with a single tier.
	ts := &fakeTierStore{snap: &TiersSnapshot{Tiers: []TierInfo{
		{
			Name:         "pro",
			Backend:      "openrouter",
			Model:        "anthropic/claude-opus-4-7",
			Tools:        []string{"bash", "grep"},
			WriteCapable: true,
			Effort:       "medium",
			MaxTurns:     12,
		},
	}}}
	e := &Engine{cfg: Config{
		Runtime:   rt,
		TierStore: ts,
		DataDir:   "/data",
	}}
	j := &Job{
		ID:       "j-llm",
		Name:     "Daily digest",
		Tier:     "pro",
		Prompt:   "summarize the day",
		Schedule: "0 9 * * *",
		Reason:   "morning check-in",
	}

	text, meta, err := e.invokeLLMViaRuntime(j)
	if err != nil {
		t.Fatalf("invokeLLMViaRuntime: %v", err)
	}
	if text != "llm said hi" {
		t.Fatalf("text: got %q", text)
	}
	if meta == nil {
		t.Fatal("meta must not be nil even when Usage is nil")
	}
	if len(rt.converseCalls) != 1 {
		t.Fatalf("Converse calls: got %d want 1", len(rt.converseCalls))
	}
	req := rt.converseCalls[0]
	if req.Backend != "openrouter" {
		t.Fatalf("Backend: got %q", req.Backend)
	}
	if string(req.Model) != "anthropic/claude-opus-4-7" {
		t.Fatalf("Model: got %q", req.Model)
	}
	if req.Effort != "medium" || !req.WriteCapable || req.MaxTurns != 12 {
		t.Fatalf("tier passthroughs: %+v", req)
	}
	if req.DataDir != "/data" {
		t.Fatalf("DataDir: got %q want /data", req.DataDir)
	}
	if len(req.Tools) != 2 || req.Tools[0].Name != "bash" {
		t.Fatalf("Tools: got %+v", req.Tools)
	}
	if len(req.SystemPrompts) == 0 {
		t.Fatal("expected job context in SystemPrompts[0]")
	}
	ctx0 := req.SystemPrompts[0]
	if !contains(ctx0, "j-llm") || !contains(ctx0, "morning check-in") {
		t.Fatalf("SystemPrompts[0] missing job context fields: %q", ctx0)
	}
}

// TestInvokeLLMViaRuntime_ErrorsOnMissingTierModel proves the single-
// ResolveModel rule survives: if the tier doesn't resolve a Model, the
// scheduler fails loudly instead of running on a silent default.
func TestInvokeLLMViaRuntime_ErrorsOnMissingTierModel(t *testing.T) {
	rt := &fakeRuntimeInvoker{}
	ts := &fakeTierStore{snap: &TiersSnapshot{Tiers: []TierInfo{
		{Name: "pro", Backend: "cli"}, // no Model
	}}}
	e := &Engine{cfg: Config{Runtime: rt, TierStore: ts}}
	j := &Job{ID: "j1", Tier: "pro", Prompt: "hi"}

	_, _, err := e.invokeLLMViaRuntime(j)
	if err == nil {
		t.Fatal("expected error when tier has no Model")
	}
	if len(rt.converseCalls) != 0 {
		t.Fatal("Converse should not be called when model is missing")
	}
}

// TestInvokeLLMWithMeta_RoutesViaRuntime confirms the dispatch: when
// Config.Runtime is set, invokeLLMWithMeta goes through Runtime.Converse
// (so scheduler jobs share the orchestration surface) rather than calling
// the legacy Provider directly.
func TestInvokeLLMWithMeta_RoutesViaRuntime(t *testing.T) {
	rt := &fakeRuntimeInvoker{converseOut: runtime.ConverseResult{Text: "ok"}}
	ts := &fakeTierStore{snap: &TiersSnapshot{Tiers: []TierInfo{{Name: "pro", Model: "m"}}}}
	e := &Engine{cfg: Config{Runtime: rt, TierStore: ts}}
	j := &Job{ID: "j", Tier: "pro", Prompt: "hi"}

	if _, _, err := e.invokeLLMWithMeta(j); err != nil {
		t.Fatalf("invokeLLMWithMeta: %v", err)
	}
	if len(rt.converseCalls) != 1 {
		t.Fatalf("Converse calls: got %d want 1", len(rt.converseCalls))
	}
}

// TestInvokeLLMWithMeta_FallsBackToLegacyWithoutRuntime: no Runtime ⇒ the
// pre-R5d Provider path is still honoured for back-compat.
func TestInvokeLLMWithMeta_FallsBackToLegacyWithoutRuntime(t *testing.T) {
	e := &Engine{cfg: Config{}} // no Runtime, no Provider
	j := &Job{ID: "j", Tier: "pro", Prompt: "hi"}
	_, _, err := e.invokeLLMWithMeta(j)
	if err == nil {
		t.Fatal("expected legacy path to fail with 'no provider configured'")
	}
}

// fakeTierStore is a local stub for #340 R5d scheduler tests.
type fakeTierStore struct{ snap *TiersSnapshot }

func (f *fakeTierStore) Current() *TiersSnapshot { return f.snap }

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestInvokeDirectCommand_RuntimeOutputErrorSurfaces covers the other failure
// mode: Invoke succeeds but Output.Error is populated (Runtime folds the
// capability's err into Output.Error on its own). The scheduler must still
// treat this as an error.
func TestInvokeDirectCommand_RuntimeOutputErrorSurfaces(t *testing.T) {
	rt := &fakeRuntimeInvoker{out: capability.Output{Error: "cap failed"}}
	e := &Engine{cfg: Config{Runtime: rt}}
	j := &Job{ID: "j-out-err", Command: "echo x"}

	_, err := e.invokeDirectCommand(j)
	if err == nil {
		t.Fatal("expected error from Output.Error")
	}
	if err.Error() != "cap failed" {
		t.Fatalf("err message: got %q want %q", err.Error(), "cap failed")
	}
}
