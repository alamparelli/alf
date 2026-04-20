package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/sandbox"
)

// Integration-style coverage for #340 R4d: drive Runtime.Chat with the real
// adapter stack (SQLiteStore in-memory + provider.NewEngine + concrete
// capability.Registry + sandbox.New()) rather than the isolated fakes used
// in impl_test.go. This is the first proof that the pieces composed in
// R3/R4a/R4b/R4c actually click together on a real database and a real
// Provider → Engine translation.

// scriptedProvider is a minimal production-shaped provider.Provider stand-in:
// it captures the last Invoke call, records its Params, and returns a
// pre-configured Result.
type scriptedProvider struct {
	lastPrompt string
	lastParams provider.Params
	result     *provider.Result
	err        error
}

func (p *scriptedProvider) Invoke(ctx context.Context, prompt string, params provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	p.lastPrompt = prompt
	p.lastParams = params
	return p.result, p.err
}

// newRealStack builds a Runtime using the concrete adapters shipped in
// R3/R4a/R4b plus an in-memory SQLite store. The stub Provider is returned
// so tests can assert on what the ai → provider translation actually sent.
func newRealStack(t *testing.T, prov *scriptedProvider, opts runtime.Options) (runtime.Runtime, *memory.SQLiteStore) {
	t.Helper()
	store, err := memory.NewSQLiteStore("") // "" ⇒ :memory:
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rt, err := runtime.New(runtime.Deps{
		Registry: capability.NewRegistry(),
		Memory:   store,
		AI:       provider.NewEngine(prov),
		Sandbox:  sandbox.New(),
	}, opts)
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	return rt, store
}

// A single-turn chat must:
//   - stream Result.Text to the caller as ai.EventToken → runtime.EventToken
//   - persist user + assistant messages to the real store
//   - propagate Model through to Provider.Invoke Params
func TestIntegration_SingleTurnChat(t *testing.T) {
	prov := &scriptedProvider{result: &provider.Result{Text: "hello back"}}
	rt, store := newRealStack(t, prov, runtime.Options{Model: "test-model", Tier: "pro"})

	ctx := context.Background()
	ch, err := rt.Chat(ctx, runtime.ChatRequest{ConvID: memory.ConvID("conv-integ"), UserInput: "hi"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	var tokens []string
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case runtime.EventToken:
			tokens = append(tokens, ev.Token)
		case runtime.EventDone:
			sawDone = true
		case runtime.EventError:
			t.Fatalf("unexpected EventError: %v", ev.Err)
		}
	}
	if !sawDone {
		t.Fatal("missing EventDone")
	}
	if joined := strings.Join(tokens, ""); joined != "hello back" {
		t.Fatalf("token concat: got %q want %q", joined, "hello back")
	}

	// Real Store verification.
	msgs, err := store.ListMessages(ctx, "conv-integ", memory.ListOpts{ApplySummary: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("persisted msg count: got %d want 2", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("msg[0].Role: got %q want user", msgs[0].Role)
	}
	// The assistant's stored text is reconstructed from the block list.
	if asst := firstTextBlock(msgs[1]); asst != "hello back" {
		t.Fatalf("assistant text: got %q want %q", asst, "hello back")
	}

	// Provider translation check.
	if prov.lastPrompt != "hi" {
		t.Fatalf("Invoke prompt: got %q want hi", prov.lastPrompt)
	}
	if prov.lastParams.Model != "test-model" {
		t.Fatalf("Invoke params Model: got %q want test-model", prov.lastParams.Model)
	}
}

// A second turn on the same convID must send the prior user/assistant
// exchange to the Provider as ConvMessages — proving history load + flatten
// + adapter split all hold end-to-end.
func TestIntegration_SecondTurnSeesPriorHistory(t *testing.T) {
	prov := &scriptedProvider{result: &provider.Result{Text: "first"}}
	rt, _ := newRealStack(t, prov, runtime.Options{Model: "m", Tier: "pro"})

	ctx := context.Background()

	drainAll := func(ch <-chan runtime.Event) {
		for range ch {
		}
	}

	ch1, err := rt.Chat(ctx, runtime.ChatRequest{ConvID: "c-2", UserInput: "one"})
	if err != nil {
		t.Fatalf("Chat #1: %v", err)
	}
	drainAll(ch1)

	// Swap provider script for the second turn.
	prov.result = &provider.Result{Text: "second"}

	ch2, err := rt.Chat(ctx, runtime.ChatRequest{ConvID: "c-2", UserInput: "two"})
	if err != nil {
		t.Fatalf("Chat #2: %v", err)
	}
	drainAll(ch2)

	// The prompt for turn 2 is "two" and history must contain both turn-1
	// messages: the original user "one" and the assistant "first".
	if prov.lastPrompt != "two" {
		t.Fatalf("turn 2 prompt: got %q want two", prov.lastPrompt)
	}
	hist := prov.lastParams.ConvMessages
	if len(hist) < 2 {
		t.Fatalf("turn 2 history len: got %d want >=2 (user one + assistant first)", len(hist))
	}
	var sawUserOne, sawAsstFirst bool
	for _, m := range hist {
		if m.Role == "user" && m.Content == "one" {
			sawUserOne = true
		}
		if m.Role == "assistant" && m.Content == "first" {
			sawAsstFirst = true
		}
	}
	if !sawUserOne || !sawAsstFirst {
		t.Fatalf("history missing expected entries: userOne=%v asstFirst=%v hist=%+v",
			sawUserOne, sawAsstFirst, hist)
	}
}

// Provider errors must surface as runtime.EventError and must NOT persist
// an assistant message — the turn is effectively abandoned server-side.
func TestIntegration_ProviderErrorDoesNotPersistAssistant(t *testing.T) {
	prov := &scriptedProvider{err: errors.New("upstream down")}
	rt, store := newRealStack(t, prov, runtime.Options{Model: "m", Tier: "pro"})

	ctx := context.Background()
	ch, err := rt.Chat(ctx, runtime.ChatRequest{ConvID: "c-err", UserInput: "please fail"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	var errEv runtime.Event
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case runtime.EventError:
			errEv = ev
		case runtime.EventDone:
			sawDone = true
		}
	}
	if errEv.Err == nil {
		t.Fatal("expected EventError")
	}
	if sawDone {
		t.Fatal("should not see EventDone after provider error")
	}

	msgs, err := store.ListMessages(ctx, "c-err", memory.ListOpts{ApplySummary: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	// The user message was persisted before Invoke ran (that is the
	// runtime contract). The assistant MUST NOT be persisted on error.
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("expected only the user msg to be persisted, got %+v", msgs)
	}
}

// TestIntegration_ConverseThroughProviderStack validates #340 R5c on the real
// adapter: Runtime.Converse → provider.NewEngine → scriptedProvider. No
// Memory write, Usage populated from Result, text aggregated from Invoke.
func TestIntegration_ConverseThroughProviderStack(t *testing.T) {
	prov := &scriptedProvider{result: &provider.Result{
		Text:      "response text",
		Model:     "actual-model",
		CostUSD:   0.002,
		NumTurns:  2,
		SessionID: "sess-abc",
	}}
	rt, store := newRealStack(t, prov, runtime.Options{Model: "opts-model", Tier: "pro"})

	res, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		SystemPrompts: []string{"you are a scheduled job"},
		Prompt:        "do the thing",
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if res.Text != "response text" {
		t.Fatalf("Text: got %q want %q", res.Text, "response text")
	}
	if res.Usage == nil || res.Usage.CostUSD != 0.002 || res.Usage.SessionID != "sess-abc" {
		t.Fatalf("Usage: got %+v", res.Usage)
	}

	// Statelessness: the in-memory store must have zero conversations.
	convs, _ := store.ListConvs(context.Background(), memory.ConvFilter{})
	if len(convs) != 0 {
		t.Fatalf("Converse must not persist; got %d conversations", len(convs))
	}

	// Provider translation: SystemPrompts flowed through, prompt is the
	// final user message.
	if prov.lastPrompt != "do the thing" {
		t.Fatalf("prompt: got %q", prov.lastPrompt)
	}
	if len(prov.lastParams.SystemPrompts) != 1 || prov.lastParams.SystemPrompts[0] != "you are a scheduled job" {
		t.Fatalf("SystemPrompts: got %v", prov.lastParams.SystemPrompts)
	}
}

// firstTextBlock returns the first BlockText Text in the message, or "".
// Mirrors the way the chat UI reconstructs text for display — useful sanity
// check that Runtime's block packing composes with the Store's round-trip.
func firstTextBlock(m memory.Message) string {
	for _, b := range m.Blocks {
		if b.Type == memory.BlockText && b.Text != "" {
			return b.Text
		}
	}
	return ""
}
