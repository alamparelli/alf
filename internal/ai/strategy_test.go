package ai_test

import (
	"context"
	"testing"

	"github.com/alamparelli/alf/internal/ai"
)

// singleShotStrategy runs the underlying engine exactly once and forwards
// every event. It is the simplest possible Strategy — used here only to
// pin the contract at compile time until R2 lands the real strategies.
type singleShotStrategy struct{}

func (singleShotStrategy) Run(ctx context.Context, engine ai.Engine, req ai.Request) (<-chan ai.Event, error) {
	return engine.Run(ctx, req)
}

// Compile-time assertion: singleShotStrategy satisfies ai.Strategy.
var _ ai.Strategy = singleShotStrategy{}

func TestStrategy_DrivesUnderlyingEngine(t *testing.T) {
	eng := &stubEngine{script: []ai.Event{
		{Kind: ai.EventToken, Token: "a"},
		{Kind: ai.EventToken, Token: "b"},
		{Kind: ai.EventDone},
	}}

	ch, err := singleShotStrategy{}.Run(context.Background(), eng, ai.Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("Strategy.Run returned error: %v", err)
	}

	var collected []string
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case ai.EventToken:
			collected = append(collected, ev.Token)
		case ai.EventDone:
			sawDone = true
		}
	}

	if got := len(collected); got != 2 {
		t.Fatalf("token count: got %d, want 2", got)
	}
	if collected[0]+collected[1] != "ab" {
		t.Fatalf("token concat: got %q, want %q", collected[0]+collected[1], "ab")
	}
	if !sawDone {
		t.Fatal("never saw EventDone")
	}
}

// retryStrategy demonstrates a Strategy that can call engine.Run more than
// once per turn — the key capability that distinguishes a Strategy from
// an Engine. We do not ship this strategy; it exists solely to prove the
// contract supports multi-call orchestration at compile time.
type retryStrategy struct {
	maxAttempts int
}

func (r retryStrategy) Run(ctx context.Context, engine ai.Engine, req ai.Request) (<-chan ai.Event, error) {
	out := make(chan ai.Event, 16)
	go func() {
		defer close(out)
		for attempt := 0; attempt < r.maxAttempts; attempt++ {
			in, err := engine.Run(ctx, req)
			if err != nil {
				out <- ai.Event{Kind: ai.EventError, Err: err}
				return
			}
			errored := false
			for ev := range in {
				if ev.Kind == ai.EventError {
					errored = true
					break
				}
				out <- ev
				if ev.Kind == ai.EventDone {
					return
				}
			}
			if !errored {
				return
			}
		}
	}()
	return out, nil
}

var _ ai.Strategy = retryStrategy{}

func TestStrategy_MayCallEngineMultipleTimes(t *testing.T) {
	// The contract allows (but does not require) a Strategy to call Engine.Run
	// more than once. We verify this compiles and behaves: retryStrategy with
	// maxAttempts=2 should complete on a healthy engine after one attempt.
	eng := &stubEngine{script: []ai.Event{
		{Kind: ai.EventToken, Token: "ok"},
		{Kind: ai.EventDone},
	}}

	ch, err := retryStrategy{maxAttempts: 2}.Run(context.Background(), eng, ai.Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("Strategy.Run returned error: %v", err)
	}

	var tokens int
	for ev := range ch {
		if ev.Kind == ai.EventToken {
			tokens++
		}
	}
	if tokens != 1 {
		t.Fatalf("tokens: got %d, want 1", tokens)
	}
}
