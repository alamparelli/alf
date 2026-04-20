package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/runtime"
)

// fakeRuntimeInvoker captures the last Invoke call and returns a scripted
// Output/error. Narrow enough to satisfy scheduler.RuntimeInvoker without
// building a real runtime.Runtime.
type fakeRuntimeInvoker struct {
	calls []fakeRuntimeCall
	out   capability.Output
	err   error
}

type fakeRuntimeCall struct {
	capID capability.ID
	args  runtime.Args
}

func (f *fakeRuntimeInvoker) Invoke(_ context.Context, capID capability.ID, args runtime.Args) (capability.Output, error) {
	f.calls = append(f.calls, fakeRuntimeCall{capID: capID, args: args})
	return f.out, f.err
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
