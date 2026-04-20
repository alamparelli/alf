package ai_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/ai"
)

// stubEngine is a minimal ai.Engine implementation used to pin the interface
// shape at compile time and to exercise the streaming contract. A2 will
// replace it with the provider-backed engine once provider/ is absorbed.
type stubEngine struct {
	script []ai.Event
}

func (e *stubEngine) Run(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	if req.Model == "" {
		return nil, errors.New("model required")
	}
	out := make(chan ai.Event, len(e.script))
	go func() {
		defer close(out)
		for _, ev := range e.script {
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}

// Compile-time check: stubEngine satisfies ai.Engine.
var _ ai.Engine = (*stubEngine)(nil)

func TestEngine_StreamsScriptedEvents(t *testing.T) {
	eng := &stubEngine{script: []ai.Event{
		{Kind: ai.EventToken, Token: "hello "},
		{Kind: ai.EventToken, Token: "world"},
		{Kind: ai.EventDone},
	}}

	ch, err := eng.Run(context.Background(), ai.Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var tokens []string
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case ai.EventToken:
			tokens = append(tokens, ev.Token)
		case ai.EventDone:
			sawDone = true
		}
	}

	if got := len(tokens); got != 2 {
		t.Fatalf("tokens: got %d, want 2", got)
	}
	if tokens[0]+tokens[1] != "hello world" {
		t.Fatalf("token concat: got %q, want %q", tokens[0]+tokens[1], "hello world")
	}
	if !sawDone {
		t.Fatalf("never saw EventDone")
	}
}

func TestEngine_EmitsToolCall(t *testing.T) {
	call := &ai.ToolCall{ID: "call_1", Name: "bash", Args: map[string]any{"cmd": "ls"}}
	eng := &stubEngine{script: []ai.Event{
		{Kind: ai.EventToolCall, ToolCall: call},
		{Kind: ai.EventDone},
	}}

	ch, err := eng.Run(context.Background(), ai.Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var got *ai.ToolCall
	for ev := range ch {
		if ev.Kind == ai.EventToolCall {
			got = ev.ToolCall
		}
	}

	if got == nil {
		t.Fatal("no ToolCall event received")
	}
	if got.ID != call.ID || got.Name != call.Name {
		t.Fatalf("ToolCall mismatch: got %+v, want %+v", got, call)
	}
	if got.Args["cmd"] != "ls" {
		t.Fatalf("ToolCall.Args: got %+v, want cmd=ls", got.Args)
	}
}

func TestEngine_ContextCancelStopsStream(t *testing.T) {
	eng := &stubEngine{script: []ai.Event{
		{Kind: ai.EventToken, Token: "a"},
		{Kind: ai.EventToken, Token: "b"},
		{Kind: ai.EventToken, Token: "c"},
		{Kind: ai.EventDone},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := eng.Run(ctx, ai.Request{Model: "test-model"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	cancel()

	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("channel not closed after context cancel")
		}
	}
}

func TestEngine_RejectsMissingModel(t *testing.T) {
	eng := &stubEngine{}
	if _, err := eng.Run(context.Background(), ai.Request{}); err == nil {
		t.Fatal("expected error when Request.Model is empty")
	}
}
