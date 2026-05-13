// Lifecycle revocation timing tests pin the #396 acceptance criterion
// from docs/ARCHITECTURE-SECURITY.md §8 deliverable 1: Instance.Close()
// cancels lifecycleCtx, and in-flight HTTP / Exec / Tool operations
// unwind via ctx cancellation within 100ms.
//
// Existing per-handle revocation tests (TestExecHandle_LifecycleCancelsInFlight,
// TestToolHandle_LifecycleCancelsInFlight) cover correctness with a
// generous 2–3s timeout. This file pins the tighter SLA — drift toward
// "ops drain instead of cancel" would surface here as a flaky 100ms
// budget exceeded.
//
// The bound is intentionally loose enough to absorb CI scheduler jitter
// and goroutine startup cost; it is tight enough that any code path
// adding a synchronous wait inside Close() (file-flush, lock-drain,
// rpc-finish) trips the test.
package handle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// closeTimingBudget is the per-op SLA. #396 spec says 100ms; we use a
// 200ms ceiling so a single 50ms scheduler hiccup on CI doesn't false-
// fail the test. Anything that takes longer than this is a real
// regression — synchronous drain in Close() is the most likely cause.
const closeTimingBudget = 200 * time.Millisecond

// TestCloseTiming_HTTP pins the SLA for in-flight HTTP requests. The
// test server holds the response open for 5s; Close should interrupt
// the request via context cancellation within the budget, regardless
// of the server-side delay.
func TestCloseTiming_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hold the response open until the client disconnects.
		<-r.Context().Done()
	}))
	defer srv.Close()

	srvURL, _ := url.Parse(srv.URL)
	h := NewHTTPHandle("cap", HTTPScope{Patterns: []HTTPPattern{{Host: srvURL.Host}}}, srv.Client())
	inst := NewInstance(context.Background(), "cap", Grants{HTTP: h})

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		resp, err := inst.HTTP.Do(context.Background(), req)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		done <- result{err: err}
	}()

	// Give the goroutine a chance to actually issue the request before
	// we Close — without this the test could pass trivially because the
	// op hasn't started yet.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	inst.Close()

	select {
	case r := <-done:
		elapsed := time.Since(start)
		if elapsed > closeTimingBudget {
			t.Errorf("HTTP request took %v to unwind after Close() — budget %v (#396 deliverable 1, see ARCHITECTURE-SECURITY.md §8)", elapsed, closeTimingBudget)
		}
		if r.err == nil {
			t.Error("HTTP request returned nil error after Close — expected context-cancelled-style error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("HTTP request did not return within 2s after Close — Close-on-Cancel wiring broken")
	}
}

// TestCloseTiming_Exec pins the SLA for in-flight subprocesses. Reuses
// the existing execBin helper from exec_test.go. A `sleep 30` is
// started; Close should reach the subprocess via ctx cancellation +
// SIGKILL within the budget.
func TestCloseTiming_Exec(t *testing.T) {
	sleep := execBin(t, "sleep")
	h := NewExecHandle("cap", ExecScope{Binaries: []string{sleep}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = inst.Exec.Run(context.Background(), sleep, []string{"30"}, nil)
	}()

	// Wait for the subprocess to actually start.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	inst.Close()

	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed > closeTimingBudget {
			t.Errorf("Exec took %v to unwind after Close() — budget %v", elapsed, closeTimingBudget)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not unwind within 3s after Close")
	}
}

// stallingInvokerTiming is a ToolInvoker that blocks on Invoke until
// the caller's ctx is cancelled. Used to prove ToolHandle propagates
// the lifecycle ctx into Invoke().
type stallingInvokerTiming struct {
	started chan struct{}
	once    atomicOnce
}

func (s *stallingInvokerTiming) Invoke(ctx context.Context, target capability.ID, in capability.Input) (capability.Output, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return capability.Output{}, ctx.Err()
}

// TestCloseTiming_Tool pins the SLA for in-flight inter-cap invocations.
// The stalling invoker holds Invoke open until the ctx fires; Close
// must surface ctx cancellation to the invoker within the budget.
func TestCloseTiming_Tool(t *testing.T) {
	inv := &stallingInvokerTiming{started: make(chan struct{})}
	h := NewToolHandle("cap", ToolScope{Allowed: []capability.ID{"target"}}, inv)
	inst := NewInstance(context.Background(), "cap", Grants{Tool: h})

	done := make(chan struct{})
	var invokeErr atomic.Value
	go func() {
		defer close(done)
		_, err := inst.Tool.Invoke(context.Background(), "target", capability.Input{})
		if err != nil {
			invokeErr.Store(err)
		}
	}()

	// Wait for Invoke to actually enter the stall.
	<-inv.started

	start := time.Now()
	inst.Close()

	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed > closeTimingBudget {
			t.Errorf("Tool.Invoke took %v to unwind — budget %v", elapsed, closeTimingBudget)
		}
		if invokeErr.Load() == nil {
			t.Error("Tool.Invoke returned nil error — expected ctx-cancelled or ErrRevoked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Tool.Invoke did not unwind within 2s after Close")
	}
}

// TestCloseTiming_ConcurrentCloseSafe verifies that simultaneous
// Close() calls do not deadlock or double-cancel. Important because
// future revocation pathways (key-based RevokeByKey from #396 commit 2)
// will Close() multiple Instances in parallel.
func TestCloseTiming_ConcurrentCloseSafe(t *testing.T) {
	const N = 50
	inst := NewInstance(context.Background(), "cap", Grants{})

	start := make(chan struct{})
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func() {
			<-start
			inst.Close()
			done <- struct{}{}
		}()
	}

	close(start)
	t0 := time.Now()
	for i := 0; i < N; i++ {
		select {
		case <-done:
		case <-time.After(closeTimingBudget):
			t.Fatalf("concurrent Close()×%d did not all return within %v (i=%d)", N, closeTimingBudget, i)
		}
	}
	elapsed := time.Since(t0)
	if elapsed > closeTimingBudget {
		t.Errorf("concurrent Close()×%d total elapsed %v > budget %v", N, elapsed, closeTimingBudget)
	}

	// Subsequent ops on the closed instance must surface ErrRevoked.
	if inst.Context().Err() == nil {
		t.Error("Instance.Context() not cancelled after concurrent Close")
	}
}
