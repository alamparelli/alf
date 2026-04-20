package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/runtime"
)

// stubRuntime is a minimal Runtime used to pin the interface shape at
// compile time and exercise the streaming contract. R2/R3 replace it with
// the real orchestrator once classifier + agents migrate into this package.
type stubRuntime struct {
	chatScript   []runtime.Event
	invokeOut    runtime.Output
	invokeErr    error
	invokeCalled bool
}

func (s *stubRuntime) Chat(ctx context.Context, req runtime.ChatRequest) (<-chan runtime.Event, error) {
	if req.ConvID == "" {
		return nil, errors.New("convID required")
	}
	if req.UserInput == "" {
		return nil, errors.New("userInput required")
	}
	out := make(chan runtime.Event, len(s.chatScript))
	go func() {
		defer close(out)
		for _, ev := range s.chatScript {
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}

func (s *stubRuntime) Invoke(ctx context.Context, capID capability.ID, args runtime.Args) (runtime.Output, error) {
	s.invokeCalled = true
	if capID == "" {
		return runtime.Output{}, errors.New("capID required")
	}
	return s.invokeOut, s.invokeErr
}

func (s *stubRuntime) Converse(ctx context.Context, req runtime.ConverseRequest) (runtime.ConverseResult, error) {
	// Stub: tests don't exercise Converse via this double. The concrete
	// Runtime has its own Converse coverage in impl_test.go / integration_test.go.
	return runtime.ConverseResult{}, errors.New("stubRuntime: Converse not implemented")
}

func (s *stubRuntime) ConverseStream(ctx context.Context, req runtime.ConverseRequest) (<-chan runtime.Event, error) {
	// Same rationale as Converse — contract pin only.
	return nil, errors.New("stubRuntime: ConverseStream not implemented")
}

// Compile-time check: stubRuntime satisfies runtime.Runtime.
var _ runtime.Runtime = (*stubRuntime)(nil)

func TestRuntime_ChatStreamsScriptedEvents(t *testing.T) {
	rt := &stubRuntime{chatScript: []runtime.Event{
		{Kind: runtime.EventToken, Token: "hello "},
		{Kind: runtime.EventToken, Token: "world"},
		{Kind: runtime.EventDone},
	}}

	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: memory.ConvID("conv-1"), UserInput: "hi"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	var tokens []string
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case runtime.EventToken:
			tokens = append(tokens, ev.Token)
		case runtime.EventDone:
			sawDone = true
		}
	}

	if len(tokens) != 2 || tokens[0]+tokens[1] != "hello world" {
		t.Fatalf("tokens: got %v", tokens)
	}
	if !sawDone {
		t.Fatal("never saw EventDone")
	}
}

func TestRuntime_ChatEmitsToolResult(t *testing.T) {
	rt := &stubRuntime{chatScript: []runtime.Event{
		{
			Kind:       runtime.EventToolResult,
			ToolName:   "bash",
			ToolResult: &capability.Output{Data: "ok", Error: ""},
		},
		{Kind: runtime.EventDone},
	}}

	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: memory.ConvID("conv-1"), UserInput: "run ls"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	var got *capability.Output
	var gotName string
	for ev := range ch {
		if ev.Kind == runtime.EventToolResult {
			got = ev.ToolResult
			gotName = ev.ToolName
		}
	}
	if got == nil {
		t.Fatal("no ToolResult event received")
	}
	if gotName != "bash" || got.Data != "ok" {
		t.Fatalf("ToolResult mismatch: name=%q data=%+v", gotName, got.Data)
	}
}

func TestRuntime_ChatContextCancelStopsStream(t *testing.T) {
	rt := &stubRuntime{chatScript: []runtime.Event{
		{Kind: runtime.EventToken, Token: "a"},
		{Kind: runtime.EventToken, Token: "b"},
		{Kind: runtime.EventToken, Token: "c"},
		{Kind: runtime.EventDone},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.Chat(ctx, runtime.ChatRequest{ConvID: memory.ConvID("conv-1"), UserInput: "hi"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	cancel()

	for range ch {
		// Drain — the goroutine must close the channel after ctx cancel.
	}
}

func TestRuntime_ChatRejectsMissingInputs(t *testing.T) {
	rt := &stubRuntime{}
	if _, err := rt.Chat(context.Background(), runtime.ChatRequest{UserInput: "hi"}); err == nil {
		t.Fatal("expected error when convID empty")
	}
	if _, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: memory.ConvID("c")}); err == nil {
		t.Fatal("expected error when userInput empty")
	}
}

func TestRuntime_InvokeReturnsOutput(t *testing.T) {
	want := runtime.Output{Data: 42, Error: ""}
	rt := &stubRuntime{invokeOut: want}

	got, err := rt.Invoke(context.Background(), capability.ID("native.echo"), runtime.Args{"x": 42})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if !rt.invokeCalled {
		t.Fatal("Invoke was not called")
	}
	if got.Data != want.Data {
		t.Fatalf("Output mismatch: got %+v, want %+v", got, want)
	}
}

func TestRuntime_InvokeSurfacesError(t *testing.T) {
	boom := errors.New("boom")
	rt := &stubRuntime{invokeErr: boom}

	_, err := rt.Invoke(context.Background(), capability.ID("native.fail"), nil)
	if !errors.Is(err, boom) {
		t.Fatalf("error mismatch: got %v, want %v", err, boom)
	}
}

func TestRuntime_InvokeRejectsMissingCapID(t *testing.T) {
	rt := &stubRuntime{}
	if _, err := rt.Invoke(context.Background(), capability.ID(""), nil); err == nil {
		t.Fatal("expected error when capID empty")
	}
}
