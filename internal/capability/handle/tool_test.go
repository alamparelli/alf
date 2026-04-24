package handle

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// stubInvoker records the last call and returns a canned response. Used
// to verify scope-passed calls actually reach the invoker, and to
// observe which toolID + input was dispatched.
type stubInvoker struct {
	calls  atomic.Int32
	lastID capability.ID
	result capability.Output
	err    error
}

func (s *stubInvoker) Invoke(ctx context.Context, id capability.ID, in capability.Input) (capability.Output, error) {
	s.calls.Add(1)
	s.lastID = id
	if s.err != nil {
		return capability.Output{}, s.err
	}
	return s.result, nil
}

// stallingInvoker blocks until ctx is cancelled, letting us verify
// lifecycle-based abort on in-flight invocations.
type stallingInvoker struct {
	started chan struct{}
}

func (s *stallingInvoker) Invoke(ctx context.Context, id capability.ID, in capability.Input) (capability.Output, error) {
	close(s.started)
	<-ctx.Done()
	return capability.Output{}, ctx.Err()
}

func TestToolScope_ExactMatch(t *testing.T) {
	s := ToolScope{Allowed: []capability.ID{"read_file", "write_file"}}
	if !s.Allows("read_file") {
		t.Error("read_file denied")
	}
	if !s.Allows("write_file") {
		t.Error("write_file denied")
	}
	if s.Allows("grep") {
		t.Error("grep accepted despite not being declared")
	}
	if s.Allows("") {
		t.Error("empty id accepted")
	}
}

func TestToolScope_EmptyDeniesAll(t *testing.T) {
	s := ToolScope{}
	if s.Allows("anything") {
		t.Error("empty Allowed must deny all")
	}
}

func TestToolHandle_InvokeInScope(t *testing.T) {
	inv := &stubInvoker{result: capability.Output{Data: "ok"}}
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"read_file"}}, inv)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})
	defer inst.Close()

	out, err := inst.Tool.Invoke(context.Background(), "read_file", capability.Input{"path": "x"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Data != "ok" {
		t.Errorf("Data=%v, want ok", out.Data)
	}
	if inv.calls.Load() != 1 {
		t.Errorf("invoker calls=%d, want 1", inv.calls.Load())
	}
	if inv.lastID != "read_file" {
		t.Errorf("lastID=%q, want read_file", inv.lastID)
	}
}

func TestToolHandle_OutOfScope(t *testing.T) {
	inv := &stubInvoker{}
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"read_file"}}, inv)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})
	defer inst.Close()

	_, err := inst.Tool.Invoke(context.Background(), "write_file", capability.Input{})
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
	if inv.calls.Load() != 0 {
		t.Errorf("invoker was called despite out-of-scope, calls=%d", inv.calls.Load())
	}
}

func TestToolHandle_InvokerErrorSurfaced(t *testing.T) {
	sentinel := errors.New("tool failed")
	inv := &stubInvoker{err: sentinel}
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"t"}}, inv)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})
	defer inst.Close()

	_, err := inst.Tool.Invoke(context.Background(), "t", capability.Input{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("invoker error must be surfaced, got %v", err)
	}
}

func TestToolHandle_NilInvokerDenies(t *testing.T) {
	// A handle constructed without an invoker is a degenerate state — it
	// must not succeed silently with a zero Output. Scope-allowed calls
	// return ErrOutOfScope to match the "has no way to reach it" semantic.
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"t"}}, nil)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})
	defer inst.Close()

	_, err := inst.Tool.Invoke(context.Background(), "t", capability.Input{})
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("nil invoker must refuse, got %v", err)
	}
}

func TestToolHandle_Revocation(t *testing.T) {
	inv := &stubInvoker{}
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"t"}}, inv)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})

	start := time.Now()
	inst.Close()

	_, err := inst.Tool.Invoke(context.Background(), "t", capability.Input{})
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("revocation took %v, want <100ms", elapsed)
	}
}

func TestToolHandle_LifecycleCancelsInFlight(t *testing.T) {
	inv := &stallingInvoker{started: make(chan struct{})}
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"t"}}, inv)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})

	var invokeErr atomic.Value
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := inst.Tool.Invoke(context.Background(), "t", capability.Input{})
		if err != nil {
			invokeErr.Store(err)
		}
	}()

	<-inv.started
	inst.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight invocation was not cancelled within 2s")
	}
	if invokeErr.Load() == nil {
		t.Fatal("expected an error from the cancelled invocation, got nil")
	}
}

func TestToolHandle_NonSerializable(t *testing.T) {
	h := NewToolHandle("cap", ToolScope{}, nil)
	if _, err := json.Marshal(h); err == nil {
		t.Fatal("ToolHandle must not be JSON-serializable")
	}
}

func TestToolHandle_Owner(t *testing.T) {
	h := NewToolHandle("cap-xyz", ToolScope{}, nil)
	if got := h.Owner(); string(got) != "cap-xyz" {
		t.Errorf("Owner()=%q, want cap-xyz", got)
	}
}
