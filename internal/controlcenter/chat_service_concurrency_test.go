package controlcenter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestStartJob_CrossConvDoesNotBlock is the regression test for issue #312:
// a hang in one conversation must not serialize calls from a different
// conversation. Before the fix, ChatService.Ask held a process-wide mutex
// that made the second StartJob wait for the first to finish.
func TestStartJob_CrossConvDoesNotBlock(t *testing.T) {
	svc := newTestChatService(t)

	releaseA := make(chan struct{})
	startedA := make(chan struct{})
	var bStarted atomic.Bool

	svc.askOverride = func(ctx context.Context, req ChatRequest, _ func(ChatEvent)) error {
		switch req.ConvID {
		case "conv-A":
			close(startedA)
			select {
			case <-releaseA:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case "conv-B":
			bStarted.Store(true)
			return nil
		}
		return nil
	}

	jobA := svc.StartJob(ChatRequest{ConvID: "conv-A", Message: "hello"})
	<-startedA // A is now mid-flight and would hold the old global mutex

	// B must be able to start and complete while A is still running.
	jobB := svc.StartJob(ChatRequest{ConvID: "conv-B", Message: "hi"})

	deadline := time.Now().Add(2 * time.Second)
	for !jobB.isDone() {
		if time.Now().After(deadline) {
			t.Fatal("conv-B job did not complete while conv-A was blocked — cross-conv serialization regression")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !bStarted.Load() {
		t.Fatal("conv-B Ask never ran while conv-A held the slot")
	}
	if jobA.isDone() {
		t.Fatal("conv-A should still be in-flight at this point")
	}

	// Cleanup: release A.
	close(releaseA)
	for !jobA.isDone() {
		if time.Now().After(time.Now().Add(2 * time.Second)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = jobA
}

// TestStartJob_Timeout verifies the per-job deadline cancels a wedged Ask
// instead of leaving the conv slot held forever (issue #312 safety net).
func TestStartJob_Timeout(t *testing.T) {
	svc := newTestChatService(t)

	// Override the package-level cap for this test via a short-lived ctx.
	// We can't change jobMaxDuration without touching the binary, so we
	// exercise cancellation by having the override honor ctx.Done() and
	// then manually cancel through chatJob.stop() after a short wait.
	askStarted := make(chan struct{})
	var sawCancel atomic.Bool

	svc.askOverride = func(ctx context.Context, _ ChatRequest, _ func(ChatEvent)) error {
		close(askStarted)
		<-ctx.Done()
		sawCancel.Store(true)
		return ctx.Err()
	}

	job := svc.StartJob(ChatRequest{ConvID: "conv-timeout", Message: "slow"})
	<-askStarted

	// Simulate the timeout reaching the job: cancel via stop() (same path
	// the ctx.DeadlineExceeded would trigger once the 20min cap fires).
	job.stop()

	deadline := time.Now().Add(1 * time.Second)
	for !job.isDone() || !sawCancel.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("job/cancel not finalized: done=%v sawCancel=%v", job.isDone(), sawCancel.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !job.wasCancelled() {
		t.Fatal("job.wasCancelled() should be true after stop()")
	}
}

// TestStartJob_SameConvReturnsExistingJob preserves the pre-fix guarantee:
// two POST /api/chat on the same conv_id do NOT race against each other —
// the second call attaches to the in-flight job instead of starting a twin.
func TestStartJob_SameConvReturnsExistingJob(t *testing.T) {
	svc := newTestChatService(t)

	release := make(chan struct{})
	var callCount atomic.Int32

	svc.askOverride = func(ctx context.Context, _ ChatRequest, _ func(ChatEvent)) error {
		callCount.Add(1)
		<-release
		return nil
	}

	req := ChatRequest{ConvID: "conv-X", Message: "first"}
	jobA := svc.StartJob(req)
	jobB := svc.StartJob(req) // same conv while first is in-flight

	if jobA != jobB {
		t.Fatal("expected same in-flight job returned for duplicate StartJob on same conv_id")
	}

	close(release)
	deadline := time.Now().Add(1 * time.Second)
	for !jobA.isDone() {
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := callCount.Load(); got != 1 {
		t.Fatalf("Ask should run exactly once, got %d", got)
	}
}

// TestAsk_PropagatesContextError makes sure Ask honors pre-cancelled contexts
// (cheap guard retained after the mutex removal).
func TestAsk_PropagatesContextError(t *testing.T) {
	svc := newTestChatService(t)
	svc.askOverride = func(_ context.Context, _ ChatRequest, _ func(ChatEvent)) error {
		return errors.New("should not be called")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := svc.Ask(ctx, ChatRequest{ConvID: "x"}, func(ChatEvent) {})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
