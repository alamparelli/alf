package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/sandbox"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// fakeRegistry is an in-memory CapabilityRegistry.
type fakeRegistry struct {
	caps map[capability.ID]capability.Capability
}

func newFakeRegistry(caps ...capability.Capability) *fakeRegistry {
	m := make(map[capability.ID]capability.Capability, len(caps))
	for _, c := range caps {
		m[c.Manifest().ID] = c
	}
	return &fakeRegistry{caps: m}
}

func (r *fakeRegistry) Resolve(id capability.ID) (capability.Capability, bool) {
	c, ok := r.caps[id]
	return c, ok
}

func (r *fakeRegistry) List() []capability.Manifest {
	out := make([]capability.Manifest, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c.Manifest())
	}
	return out
}

// fakeCapability is a deterministic Capability for tests. ExecFn may be nil;
// if so Execute returns Output{Data: "ok"}.
type fakeCapability struct {
	manifest capability.Manifest
	execFn   func(ctx context.Context, in capability.Input) (capability.Output, error)
	calls    int
	mu       sync.Mutex
}

func (c *fakeCapability) Manifest() capability.Manifest       { return c.manifest }
func (c *fakeCapability) Permissions() capability.PermissionSet { return c.manifest.Permissions }
func (c *fakeCapability) Execute(ctx context.Context, in capability.Input) (capability.Output, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if c.execFn != nil {
		return c.execFn(ctx, in)
	}
	return capability.Output{Data: "ok"}, nil
}

// fakeEngine scripts one ai.Event slice per Run call, so tests can model a
// multi-iteration tool loop: first Run emits a ToolCall, second Run emits
// the final EventDone.
type fakeEngine struct {
	mu       sync.Mutex
	scripts  [][]ai.Event
	requests []ai.Request
	runs     int
}

func (e *fakeEngine) Run(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	e.mu.Lock()
	e.requests = append(e.requests, req)
	if e.runs >= len(e.scripts) {
		e.mu.Unlock()
		return nil, errors.New("fakeEngine: unexpected extra Run call")
	}
	script := e.scripts[e.runs]
	e.runs++
	e.mu.Unlock()

	ch := make(chan ai.Event, len(script))
	go func() {
		defer close(ch)
		for _, ev := range script {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

// fakeStore is a minimal, test-only Store. Only the methods the Runtime calls
// during Chat / Invoke are implemented — everything else panics so a future
// expansion of the Runtime's Store dependency surfaces as a loud test failure.
type fakeStore struct {
	mu       sync.Mutex
	messages map[memory.ConvID][]memory.Message
	seq      int64
	fail     error // if non-nil, AppendMessage returns this error
}

func newFakeStore() *fakeStore {
	return &fakeStore{messages: make(map[memory.ConvID][]memory.Message)}
}

func (s *fakeStore) AppendMessage(ctx context.Context, convID memory.ConvID, msg memory.Message) (memory.Message, error) {
	if s.fail != nil {
		return memory.Message{}, s.fail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	msg.ID = memory.MsgID("m" + itoa(s.seq))
	msg.Seq = s.seq
	msg.CreatedAt = time.Now().UnixMilli()
	s.messages[convID] = append(s.messages[convID], msg)
	return msg, nil
}

func (s *fakeStore) ListMessages(ctx context.Context, convID memory.ConvID, _ memory.ListOpts) ([]memory.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.messages[convID]
	out := make([]memory.Message, len(src))
	copy(out, src)
	return out, nil
}

func (s *fakeStore) All(convID memory.ConvID) []memory.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]memory.Message(nil), s.messages[convID]...)
}

// Unused Store methods — keep as panics to catch accidental expansion.
func (s *fakeStore) EnsureConv(context.Context, memory.ConvID, string, memory.Channel) error {
	panic("fakeStore.EnsureConv: not expected in R3 tests")
}
func (s *fakeStore) GetConv(context.Context, memory.ConvID) (memory.ConvInfo, error) {
	panic("fakeStore.GetConv: not expected in R3 tests")
}
func (s *fakeStore) ListConvs(context.Context, memory.ConvFilter) ([]memory.ConvInfo, error) {
	panic("fakeStore.ListConvs: not expected in R3 tests")
}
func (s *fakeStore) UpdateConvTitle(context.Context, memory.ConvID, string) error {
	panic("fakeStore.UpdateConvTitle: not expected in R3 tests")
}
func (s *fakeStore) ArchiveConv(context.Context, memory.ConvID) error {
	panic("fakeStore.ArchiveConv: not expected in R3 tests")
}
func (s *fakeStore) DeleteConv(context.Context, memory.ConvID) error {
	panic("fakeStore.DeleteConv: not expected in R3 tests")
}
func (s *fakeStore) LatestConvID(context.Context, memory.Channel) (memory.ConvID, error) {
	panic("fakeStore.LatestConvID: not expected in R3 tests")
}
func (s *fakeStore) GetMessage(context.Context, memory.ConvID, memory.MsgID) (*memory.Message, error) {
	panic("fakeStore.GetMessage: not expected in R3 tests")
}
func (s *fakeStore) AddReaction(context.Context, memory.ConvID, memory.MsgID, memory.Reaction) (bool, error) {
	panic("fakeStore.AddReaction: not expected in R3 tests")
}
func (s *fakeStore) AppendSummary(context.Context, memory.ConvID, string, []memory.MsgID) error {
	panic("fakeStore.AppendSummary: not expected in R3 tests")
}
func (s *fakeStore) LatestSummaryCovered(context.Context, memory.ConvID) ([]memory.MsgID, error) {
	panic("fakeStore.LatestSummaryCovered: not expected in R3 tests")
}
func (s *fakeStore) Summarize(context.Context, memory.ConvID) (memory.Summary, error) {
	panic("fakeStore.Summarize: not expected in R3 tests")
}
func (s *fakeStore) Index(context.Context, memory.Scope, memory.Document) error {
	panic("fakeStore.Index: not expected in R3 tests")
}
func (s *fakeStore) Search(context.Context, memory.Scope, string, int) ([]memory.Hit, error) {
	panic("fakeStore.Search: not expected in R3 tests")
}
func (s *fakeStore) GetDocument(context.Context, memory.Scope, string) (*memory.Document, error) {
	panic("fakeStore.GetDocument: not expected in R3 tests")
}
func (s *fakeStore) DeleteDocument(context.Context, memory.Scope, string) (bool, error) {
	panic("fakeStore.DeleteDocument: not expected in R3 tests")
}
func (s *fakeStore) ListDocuments(context.Context, memory.Scope, int) ([]memory.Document, error) {
	panic("fakeStore.ListDocuments: not expected in R3 tests")
}
func (s *fakeStore) GetPref(context.Context, string) (memory.Value, error) {
	panic("fakeStore.GetPref: not expected in R3 tests")
}
func (s *fakeStore) SetPref(context.Context, string, memory.Value) error {
	panic("fakeStore.SetPref: not expected in R3 tests")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// helper: drain the event channel and collect outcomes.
type collected struct {
	tokens  []string
	tools   []runtime.Event
	doneErr error // non-nil iff the terminal event was EventError
	sawDone bool
}

func drain(ch <-chan runtime.Event) collected {
	var c collected
	for ev := range ch {
		switch ev.Kind {
		case runtime.EventToken:
			c.tokens = append(c.tokens, ev.Token)
		case runtime.EventToolResult:
			c.tools = append(c.tools, ev)
		case runtime.EventDone:
			c.sawDone = true
		case runtime.EventError:
			c.doneErr = ev.Err
		}
	}
	return c
}

// ── constructor validation ──────────────────────────────────────────────────

func TestNew_ValidatesDeps(t *testing.T) {
	base := runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}
	cases := []struct {
		name string
		mut  func(d *runtime.Deps, o *runtime.Options)
	}{
		{"missing Registry", func(d *runtime.Deps, _ *runtime.Options) { d.Registry = nil }},
		{"missing Memory", func(d *runtime.Deps, _ *runtime.Options) { d.Memory = nil }},
		{"missing AI", func(d *runtime.Deps, _ *runtime.Options) { d.AI = nil }},
		{"missing Sandbox", func(d *runtime.Deps, _ *runtime.Options) { d.Sandbox = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			o := runtime.Options{Model: "test-model"}
			tc.mut(&d, &o)
			if _, err := runtime.New(d, o); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestChat_ErrorsIfModelMissing pins the Model-required contract at Chat
// call time (relocated from runtime.New in #340 R5b so Invoke-only consumers
// can construct a Runtime without a model).
func TestChat_ErrorsIfModelMissing(t *testing.T) {
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{}) // no Model
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "hi"}); err == nil {
		t.Fatal("Chat without Model should error")
	}
}

// ── Chat pipeline ───────────────────────────────────────────────────────────

func TestChat_PersistsUserAndAssistantAndStreamsTokens(t *testing.T) {
	store := newFakeStore()
	eng := &fakeEngine{scripts: [][]ai.Event{
		{
			{Kind: ai.EventToken, Token: "hel"},
			{Kind: ai.EventToken, Token: "lo"},
			{Kind: ai.EventDone},
		},
	}}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   store,
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "conv-1", UserInput: "hi"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	got := drain(ch)

	if got.doneErr != nil {
		t.Fatalf("unexpected error event: %v", got.doneErr)
	}
	if !got.sawDone {
		t.Fatal("missing EventDone")
	}
	if join(got.tokens) != "hello" {
		t.Fatalf("tokens: got %q want %q", join(got.tokens), "hello")
	}

	msgs := store.All("conv-1")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 persisted messages (user + assistant), got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Blocks[0].Text != "hi" {
		t.Fatalf("user msg wrong: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Blocks[0].Text != "hello" {
		t.Fatalf("assistant msg wrong: %+v", msgs[1])
	}

	// Request should have carried the flattened history (user turn only).
	if len(eng.requests) != 1 {
		t.Fatalf("engine called %d times, want 1", len(eng.requests))
	}
	if len(eng.requests[0].Messages) != 1 || eng.requests[0].Messages[0].Content != "hi" {
		t.Fatalf("first request messages wrong: %+v", eng.requests[0].Messages)
	}
	if eng.requests[0].Model != "test-model" {
		t.Fatalf("model not propagated: %q", eng.requests[0].Model)
	}
}

func TestChat_ExecutesToolCallAndReinjectsResult(t *testing.T) {
	// Capability returning a fixed success result.
	echoCap := &fakeCapability{
		manifest: capability.Manifest{
			ID:          "native.echo",
			Kind:        capability.KindTool,
			Name:        "echo",
			Description: "repeat input",
		},
		execFn: func(ctx context.Context, in capability.Input) (capability.Output, error) {
			// Sandbox.Apply should have installed a Policy on ctx.
			if _, ok := sandbox.PolicyFrom(ctx); !ok {
				return capability.Output{Error: "no policy"}, nil
			}
			return capability.Output{Data: "echoed:" + toStr(in["msg"])}, nil
		},
	}

	eng := &fakeEngine{scripts: [][]ai.Event{
		// First turn: emit one ToolCall + done.
		{
			{Kind: ai.EventToken, Token: "thinking..."},
			{Kind: ai.EventToolCall, ToolCall: &ai.ToolCall{
				ID: "tc1", Name: "native.echo", Args: map[string]any{"msg": "hi"},
			}},
			{Kind: ai.EventDone},
		},
		// Second turn: plain text + done.
		{
			{Kind: ai.EventToken, Token: "final"},
			{Kind: ai.EventDone},
		},
	}}

	store := newFakeStore()
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(echoCap),
		Memory:   store,
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "conv-1", UserInput: "please echo"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	got := drain(ch)

	if got.doneErr != nil {
		t.Fatalf("unexpected error event: %v", got.doneErr)
	}
	if !got.sawDone {
		t.Fatal("missing EventDone")
	}
	if join(got.tokens) != "thinking...final" {
		t.Fatalf("tokens concat: got %q", join(got.tokens))
	}
	if len(got.tools) != 1 {
		t.Fatalf("expected 1 tool result event, got %d", len(got.tools))
	}
	if got.tools[0].ToolName != "native.echo" {
		t.Fatalf("tool name wrong: %q", got.tools[0].ToolName)
	}
	if got.tools[0].ToolResult == nil || got.tools[0].ToolResult.Data != "echoed:hi" {
		t.Fatalf("tool result wrong: %+v", got.tools[0].ToolResult)
	}

	if echoCap.calls != 1 {
		t.Fatalf("capability.Execute calls: got %d want 1", echoCap.calls)
	}
	if eng.runs != 2 {
		t.Fatalf("engine runs: got %d want 2 (one for the call, one after tool result)", eng.runs)
	}

	// The second engine request must include the tool-result message at the tail.
	second := eng.requests[1].Messages
	if len(second) == 0 {
		t.Fatal("second request has no messages")
	}
	tail := second[len(second)-1]
	if tail.Role != ai.RoleTool || tail.Content != "echoed:hi" {
		t.Fatalf("second request tail msg wrong: %+v", tail)
	}

	// Assistant persisted msg must include text + tool_use + tool_result blocks.
	msgs := store.All("conv-1")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(msgs))
	}
	asst := msgs[1]
	var sawUse, sawResult, sawText bool
	for _, b := range asst.Blocks {
		switch b.Type {
		case memory.BlockText:
			sawText = true
		case memory.BlockToolUse:
			if b.ToolID == "tc1" && b.Name == "native.echo" {
				sawUse = true
			}
		case memory.BlockToolResult:
			if b.ToolID == "tc1" && b.Output == "echoed:hi" {
				sawResult = true
			}
		}
	}
	if !sawText || !sawUse || !sawResult {
		t.Fatalf("assistant blocks incomplete: text=%v use=%v result=%v blocks=%+v",
			sawText, sawUse, sawResult, asst.Blocks)
	}
}

func TestChat_ToolCapabilityNotFound_FoldsErrorIntoResult(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{
		{
			{Kind: ai.EventToolCall, ToolCall: &ai.ToolCall{
				ID: "tc1", Name: "native.missing", Args: nil,
			}},
			{Kind: ai.EventDone},
		},
		{
			{Kind: ai.EventToken, Token: "sorry"},
			{Kind: ai.EventDone},
		},
	}}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(), // empty
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, _ := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "x"})
	got := drain(ch)
	if got.doneErr != nil {
		t.Fatalf("unexpected error: %v", got.doneErr)
	}
	if len(got.tools) != 1 || got.tools[0].ToolResult == nil || got.tools[0].ToolResult.Error == "" {
		t.Fatalf("expected tool result with error, got %+v", got.tools)
	}
}

func TestChat_AIEngineRunError_IsSurfaced(t *testing.T) {
	eng := &errEngine{err: errors.New("boom")}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "x"})
	if err != nil {
		t.Fatalf("Chat returned setup error: %v", err)
	}
	got := drain(ch)
	if got.doneErr == nil {
		t.Fatal("expected EventError but got none")
	}
	if got.sawDone {
		t.Fatal("should not have reached EventDone after an engine error")
	}
}

func TestChat_StreamErrorEvent_IsSurfaced(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{
		{
			{Kind: ai.EventToken, Token: "partial"},
			{Kind: ai.EventError, Err: errors.New("midstream")},
		},
	}}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, _ := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "x"})
	got := drain(ch)
	if got.doneErr == nil || got.doneErr.Error() != "midstream" {
		t.Fatalf("expected midstream error, got %v", got.doneErr)
	}
}

func TestChat_MaxIterations_Trips(t *testing.T) {
	// Engine always emits a ToolCall → infinite loop without the cap.
	looping := []ai.Event{
		{Kind: ai.EventToolCall, ToolCall: &ai.ToolCall{ID: "tc", Name: "native.noop"}},
		{Kind: ai.EventDone},
	}
	scripts := make([][]ai.Event, 5)
	for i := range scripts {
		scripts[i] = looping
	}
	eng := &fakeEngine{scripts: scripts}
	noopCap := &fakeCapability{
		manifest: capability.Manifest{ID: "native.noop", Kind: capability.KindTool, Name: "noop"},
	}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(noopCap),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model", MaxIterations: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, _ := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "x"})
	got := drain(ch)
	if got.doneErr == nil {
		t.Fatal("expected max-iterations error")
	}
}

func TestChat_RejectsMissingInputs(t *testing.T) {
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "", UserInput: "x"}); err == nil {
		t.Fatal("expected error for empty convID")
	}
	if _, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: ""}); err == nil {
		t.Fatal("expected error for empty userInput")
	}
}

// TestChat_ForwardsRequestPassthroughs pins the #340 R4h contract: every
// per-call field on ChatRequest flows into the ai.Request the engine sees on
// the first Run of the turn — so chat_service + comms.ChatEngine can control
// Model/Tier params per message without mutating Runtime.Options.
func TestChat_ForwardsRequestPassthroughs(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "options-fallback"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	explicitTools := []ai.ToolSpec{{Name: "picked"}}
	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{
		ConvID:        "conv-pass",
		UserInput:     "hello",
		Model:         "request-model",
		Backend:       "openrouter",
		SystemPrompts: []string{"identity", "tier-prompt"},
		Tools:         explicitTools,
		MaxTurns:      9,
		Effort:        "high",
		WriteCapable:  true,
		DataDir:       "/data/req",
		ResumeID:      "sess-chat",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}

	if len(eng.requests) != 1 {
		t.Fatalf("engine runs: got %d want 1", len(eng.requests))
	}
	r := eng.requests[0]
	if r.Model != "request-model" {
		t.Errorf("Model: got %q want request-model (options fallback must not win)", r.Model)
	}
	if r.Backend != "openrouter" || r.Effort != "high" || !r.WriteCapable || r.MaxTurns != 9 || r.DataDir != "/data/req" || r.ResumeID != "sess-chat" {
		t.Errorf("passthroughs drifted: %+v", r)
	}
	if len(r.SystemPrompts) != 2 || r.SystemPrompts[0] != "identity" || r.SystemPrompts[1] != "tier-prompt" {
		t.Errorf("SystemPrompts: %+v", r.SystemPrompts)
	}
	if len(r.Tools) != 1 || r.Tools[0].Name != "picked" {
		t.Errorf("Tools override not honoured: %+v (want [picked])", r.Tools)
	}
}

// TestChat_ModelFallsBackToOptions pins the "Options.Model is the fallback"
// half of the Converse/Chat symmetry: leaving ChatRequest.Model empty must
// pick up the Runtime-level default so existing single-tier setups still
// work without threading a Model on every call.
func TestChat_ModelFallsBackToOptions(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "options-default"})

	ch, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "hi"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for range ch {
	}
	if eng.requests[0].Model != "options-default" {
		t.Fatalf("Model fallback: got %q want options-default", eng.requests[0].Model)
	}
}

// TestChat_ErrorsWhenNoModelAnywhere pins the safety net: an empty
// ChatRequest.Model paired with an empty Options.Model is a config bug, not
// a silent run on some hidden default.
func TestChat_ErrorsWhenNoModelAnywhere(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}},
		Sandbox:  sandbox.New(),
	}, runtime.Options{})

	_, err := rt.Chat(context.Background(), runtime.ChatRequest{ConvID: "c", UserInput: "hi"})
	if err == nil {
		t.Fatal("expected error when neither Request.Model nor Options.Model is set")
	}
}

// ── Invoke pipeline ─────────────────────────────────────────────────────────

func TestInvoke_ResolvesDerivesAppliesAndExecutes(t *testing.T) {
	// Capability asserts Sandbox.Apply installed a Policy on ctx.
	cap := &fakeCapability{
		manifest: capability.Manifest{
			ID:   "native.ping",
			Kind: capability.KindTool,
			Name: "ping",
			Permissions: capability.PermissionSet{
				FilePaths: []string{"/tmp/*"},
			},
		},
		execFn: func(ctx context.Context, in capability.Input) (capability.Output, error) {
			p, ok := sandbox.PolicyFrom(ctx)
			if !ok {
				return capability.Output{}, errors.New("runtime did not install policy")
			}
			if len(p.FileAccess.ReadPaths) != 1 || p.FileAccess.ReadPaths[0] != "/tmp/*" {
				return capability.Output{}, errors.New("policy not derived from manifest permissions")
			}
			return capability.Output{Data: in["echo"]}, nil
		},
	}

	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(cap),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model", Tier: "pro"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := rt.Invoke(context.Background(), "native.ping", runtime.Args{"echo": "pong"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Data != "pong" {
		t.Fatalf("out.Data: got %+v, want \"pong\"", out.Data)
	}
	if cap.calls != 1 {
		t.Fatalf("calls: got %d want 1", cap.calls)
	}
}

func TestInvoke_UnknownCapability(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if _, err := rt.Invoke(context.Background(), "native.nope", nil); err == nil {
		t.Fatal("expected error for unknown cap")
	}
}

func TestInvoke_RejectsEmptyCapID(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "test-model"})
	if _, err := rt.Invoke(context.Background(), "", nil); err == nil {
		t.Fatal("expected error for empty capID")
	}
}

// ── #340 R5c Converse ───────────────────────────────────────────────────────

// TestConverse_AggregatesTextAndPropagatesUsage pins the one-shot surface:
// tokens concatenate into Text, Usage on EventDone flows through.
func TestConverse_AggregatesTextAndPropagatesUsage(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{
		{
			{Kind: ai.EventToken, Token: "hel"},
			{Kind: ai.EventToken, Token: "lo"},
			{Kind: ai.EventDone, Usage: &ai.Usage{CostUSD: 0.001, Model: "test-model", NumTurns: 1}},
		},
	}}
	rt, err := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "fallback-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if res.Text != "hello" {
		t.Fatalf("Text: got %q want hello", res.Text)
	}
	if res.Usage == nil || res.Usage.CostUSD != 0.001 || res.Usage.NumTurns != 1 {
		t.Fatalf("Usage: got %+v", res.Usage)
	}
}

// TestConverse_ForwardsProviderPassthroughs proves #340 R5d: Backend /
// Effort / WriteCapable / MaxTurns / DataDir flow from ConverseRequest to
// ai.Request verbatim. The Runtime does not interpret them; it just passes
// them to the Engine.
func TestConverse_ForwardsProviderPassthroughs(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt:       "hi",
		Backend:      "openrouter",
		Effort:       "high",
		WriteCapable: true,
		MaxTurns:     7,
		DataDir:      "/data",
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	req := eng.requests[0]
	if req.Backend != "openrouter" {
		t.Fatalf("Backend: got %q want openrouter", req.Backend)
	}
	if req.Effort != "high" || !req.WriteCapable || req.MaxTurns != 7 || req.DataDir != "/data" {
		t.Fatalf("passthroughs not forwarded: %+v", req)
	}
}

// TestConverse_ForwardsResumeID pins the #340 R4e passthrough: ResumeID flows
// from ConverseRequest → ai.Request so chat follow-ups can continue a
// provider-side session through Runtime.
func TestConverse_ForwardsResumeID(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt:   "continue",
		ResumeID: "sess-xyz",
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if got := eng.requests[0].ResumeID; got != "sess-xyz" {
		t.Fatalf("ResumeID: got %q want sess-xyz", got)
	}
}

// TestConverse_ForwardsSystemPromptsAndHistory shows Request.SystemPrompts +
// History land on the ai.Request the engine receives, and the Prompt is
// appended as the final user message.
func TestConverse_ForwardsSystemPromptsAndHistory(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Model:         "override-model",
		SystemPrompts: []string{"identity", "job-context"},
		Prompt:        "user turn",
		History: []ai.Message{
			{Role: ai.RoleUser, Content: "earlier q"},
			{Role: ai.RoleAssistant, Content: "earlier a"},
		},
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if len(eng.requests) != 1 {
		t.Fatalf("Run calls: got %d want 1", len(eng.requests))
	}
	req := eng.requests[0]
	if req.Model != "override-model" {
		t.Fatalf("Model: got %q want override-model", req.Model)
	}
	if len(req.SystemPrompts) != 2 || req.SystemPrompts[0] != "identity" {
		t.Fatalf("SystemPrompts: got %v", req.SystemPrompts)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("Messages len: got %d want 3", len(req.Messages))
	}
	if req.Messages[2].Role != ai.RoleUser || req.Messages[2].Content != "user turn" {
		t.Fatalf("final message: got %+v", req.Messages[2])
	}
}

// TestConverse_FallsBackToOptionsModel ensures the Options.Model seeded at
// New-time is used when Request.Model is empty — symmetric with Chat.
func TestConverse_FallsBackToOptionsModel(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "fallback"})

	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if eng.requests[0].Model != "fallback" {
		t.Fatalf("Model: got %q want fallback", eng.requests[0].Model)
	}
}

// TestConverse_ErrorsWithoutModel guards against silently running on a
// hardcoded default: no Model in Request AND no Options.Model ⇒ error.
func TestConverse_ErrorsWithoutModel(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{}) // no Model
	if _, err := rt.Converse(context.Background(), runtime.ConverseRequest{Prompt: "hi"}); err == nil {
		t.Fatal("expected error when neither Request nor Options carry Model")
	}
}

// TestConverse_ErrorsOnEmptyPrompt enforces the non-negotiable input.
func TestConverse_ErrorsOnEmptyPrompt(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})
	if _, err := rt.Converse(context.Background(), runtime.ConverseRequest{}); err == nil {
		t.Fatal("expected error on empty Prompt")
	}
}

// TestConverse_EngineErrorSurfaces propagates ai.Engine.Run errors without
// wrapping them into a ConverseResult.
func TestConverse_EngineErrorSurfaces(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &errEngine{err: errors.New("upstream")},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})
	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error from engine")
	}
}

// TestConverse_EventErrorSurfaces covers the mid-stream error case: the
// engine ran fine but emitted EventError before EventDone.
func TestConverse_EventErrorSurfaces(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventError, Err: errors.New("mid-stream")}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})
	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected mid-stream error to surface")
	}
}

// TestConverse_DoesNotPersistMemory pins the #340 R5c invariant: a Converse
// call MUST NOT touch Memory. The fake store would fail Append when it sees
// a write; absence of writes proves statelessness.
func TestConverse_DoesNotPersistMemory(t *testing.T) {
	store := newFakeStore()
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventToken, Token: "x"}, {Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   store,
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	if _, err := rt.Converse(context.Background(), runtime.ConverseRequest{Prompt: "hi"}); err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if len(store.All("any")) != 0 {
		t.Fatalf("expected 0 messages written; got %d", len(store.All("any")))
	}
}

// TestConverse_StrategyDrivesEngine proves the #340 R5e hook: when a
// Strategy is attached, Converse drives through it rather than calling
// engine.Run directly. The Strategy gets the same (engine, request) tuple
// the Runtime would have passed to Run — so wrapping orchestrators can be
// added later without touching Runtime.
func TestConverse_StrategyDrivesEngine(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{
		{
			{Kind: ai.EventToken, Token: "from "},
			{Kind: ai.EventToken, Token: "strategy"},
			{Kind: ai.EventDone, Usage: &ai.Usage{CostUSD: 0.01, NumTurns: 2}},
		},
	}}
	// The strategy records that it was called and then delegates to the
	// Engine it was given — exactly the contract.
	var strategyCalled bool
	strat := aiStrategyFunc(func(ctx context.Context, engine ai.Engine, req ai.Request) (<-chan ai.Event, error) {
		strategyCalled = true
		return engine.Run(ctx, req)
	})
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	res, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt:   "hi",
		Strategy: strat,
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if !strategyCalled {
		t.Fatal("Strategy.Run was not invoked")
	}
	if res.Text != "from strategy" {
		t.Fatalf("Text: got %q", res.Text)
	}
	if res.Usage == nil || res.Usage.NumTurns != 2 {
		t.Fatalf("Usage: got %+v", res.Usage)
	}
}

// TestConverse_StrategyBypassesEngineRun guards against a refactor that
// would accidentally call engine.Run on top of the Strategy — that would
// cause double execution. We install an engine that fails on any direct
// Run call; the Strategy returns a handcrafted stream. If Converse passes,
// the dispatch is clean.
func TestConverse_StrategyBypassesEngineRun(t *testing.T) {
	failEng := &errEngine{err: errors.New("engine.Run should not be called directly")}
	strat := aiStrategyFunc(func(ctx context.Context, _ ai.Engine, _ ai.Request) (<-chan ai.Event, error) {
		ch := make(chan ai.Event, 2)
		ch <- ai.Event{Kind: ai.EventToken, Token: "x"}
		ch <- ai.Event{Kind: ai.EventDone}
		close(ch)
		return ch, nil
	})
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       failEng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	res, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt:   "hi",
		Strategy: strat,
	})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if res.Text != "x" {
		t.Fatalf("Text: got %q", res.Text)
	}
}

// TestConverse_StrategySkipsModelCheck relaxes the single-ResolveModel
// check when a Strategy is attached — the Strategy may resolve models
// internally (e.g. multi-agent orchestrator's own tier lookup). Introduced
// in #340 R5e3.
func TestConverse_StrategySkipsModelCheck(t *testing.T) {
	called := false
	strat := aiStrategyFunc(func(_ context.Context, _ ai.Engine, _ ai.Request) (<-chan ai.Event, error) {
		called = true
		ch := make(chan ai.Event, 1)
		ch <- ai.Event{Kind: ai.EventDone}
		close(ch)
		return ch, nil
	})
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{}) // no Options.Model
	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt:   "hi",
		Strategy: strat,
		// No Request.Model either — but Strategy is set, so Converse allows it.
	})
	if err != nil {
		t.Fatalf("Converse with Strategy should not require Model: %v", err)
	}
	if !called {
		t.Fatal("Strategy was not invoked")
	}
}

// TestConverse_StrategyErrorSurfaces: a Strategy that returns a non-nil
// error on Run must propagate verbatim — Runtime does not retry or wrap
// into success.
func TestConverse_StrategyErrorSurfaces(t *testing.T) {
	strat := aiStrategyFunc(func(_ context.Context, _ ai.Engine, _ ai.Request) (<-chan ai.Event, error) {
		return nil, errors.New("strategy exploded")
	})
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{
		Prompt:   "hi",
		Strategy: strat,
	})
	if err == nil {
		t.Fatal("expected error from Strategy")
	}
}

// TestConverse_StreamClosedWithoutDoneErrors defends against a malformed
// Engine implementation that closes the channel before EventDone — the
// Runtime must not return a fake-successful empty result.
func TestConverse_StreamClosedWithoutDoneErrors(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventToken, Token: "x"}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})
	_, err := rt.Converse(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error when stream closes without EventDone")
	}
}

// aiStrategyFunc is a func-adapter so tests don't need a standalone type per
// strategy. Satisfies ai.Strategy.
type aiStrategyFunc func(ctx context.Context, engine ai.Engine, req ai.Request) (<-chan ai.Event, error)

func (f aiStrategyFunc) Run(ctx context.Context, engine ai.Engine, req ai.Request) (<-chan ai.Event, error) {
	return f(ctx, engine, req)
}

// ── ConverseStream (#340 R4i) ───────────────────────────────────────────────

// TestConverseStream_ForwardsTokensAndUsage pins the happy path: every
// ai.EventToken arrives as a runtime.EventToken, the terminal EventDone
// carries Usage through, and the channel closes cleanly.
func TestConverseStream_ForwardsTokensAndUsage(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{
		{Kind: ai.EventToken, Token: "hel"},
		{Kind: ai.EventToken, Token: "lo"},
		{Kind: ai.EventDone, Usage: &ai.Usage{CostUSD: 0.02, Model: "m", NumTurns: 1, SessionID: "s-9"}},
	}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	ch, err := rt.ConverseStream(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("ConverseStream: %v", err)
	}
	var tokens []string
	var done runtime.Event
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case runtime.EventToken:
			tokens = append(tokens, ev.Token)
		case runtime.EventDone:
			done = ev
			sawDone = true
		case runtime.EventError:
			t.Fatalf("unexpected EventError: %v", ev.Err)
		}
	}
	if join(tokens) != "hello" {
		t.Fatalf("tokens: got %q want hello", join(tokens))
	}
	if !sawDone {
		t.Fatal("missing EventDone")
	}
	if done.Usage == nil || done.Usage.SessionID != "s-9" || done.Usage.CostUSD != 0.02 {
		t.Fatalf("Usage not surfaced on EventDone: %+v", done.Usage)
	}
}

// TestConverseStream_ForwardsPassthroughs checks the shared builder is
// wired: Backend / Effort / MaxTurns / ResumeID / SystemPrompts / Tools
// all land on the ai.Request the engine receives — same contract as
// Converse, proved independently so future drift is caught.
func TestConverseStream_ForwardsPassthroughs(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{{Kind: ai.EventDone}}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "fallback"})

	ch, err := rt.ConverseStream(context.Background(), runtime.ConverseRequest{
		Prompt:        "hello",
		Model:         "override",
		Backend:       "openrouter",
		SystemPrompts: []string{"tier-prompt"},
		Tools:         []ai.ToolSpec{{Name: "bash"}},
		MaxTurns:      7,
		Effort:        "high",
		WriteCapable:  true,
		DataDir:       "/data",
		ResumeID:      "sess-stream",
	})
	if err != nil {
		t.Fatalf("ConverseStream: %v", err)
	}
	for range ch {
	}
	if len(eng.requests) != 1 {
		t.Fatalf("engine runs: got %d want 1", len(eng.requests))
	}
	r := eng.requests[0]
	if r.Model != "override" || r.Backend != "openrouter" || r.Effort != "high" || r.MaxTurns != 7 || !r.WriteCapable || r.DataDir != "/data" || r.ResumeID != "sess-stream" {
		t.Errorf("passthroughs drifted: %+v", r)
	}
	if len(r.SystemPrompts) != 1 || r.SystemPrompts[0] != "tier-prompt" {
		t.Errorf("SystemPrompts: %+v", r.SystemPrompts)
	}
	if len(r.Tools) != 1 || r.Tools[0].Name != "bash" {
		t.Errorf("Tools: %+v", r.Tools)
	}
}

// TestConverseStream_EventErrorSurfaces proves a mid-stream ai.EventError
// is forwarded as a terminal runtime.EventError (not swallowed, not
// followed by a phantom EventDone).
func TestConverseStream_EventErrorSurfaces(t *testing.T) {
	boom := errors.New("mid-stream")
	eng := &fakeEngine{scripts: [][]ai.Event{{
		{Kind: ai.EventToken, Token: "partial"},
		{Kind: ai.EventError, Err: boom},
	}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	ch, err := rt.ConverseStream(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("ConverseStream: %v", err)
	}
	var gotErr error
	var sawDone bool
	for ev := range ch {
		if ev.Kind == runtime.EventError {
			gotErr = ev.Err
		}
		if ev.Kind == runtime.EventDone {
			sawDone = true
		}
	}
	if gotErr == nil || !errors.Is(gotErr, boom) {
		t.Fatalf("expected wrapped mid-stream error, got: %v", gotErr)
	}
	if sawDone {
		t.Fatal("EventDone must not fire after EventError")
	}
}

// TestConverseStream_DropsToolCalls pins the documented translation: the
// Provider stack owns the tool loop (ToolLoop wrapper), so surfacing
// ai.EventToolCall at the ConverseStream boundary would encourage double
// execution. Tool calls must not reach the consumer here.
func TestConverseStream_DropsToolCalls(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{
		{Kind: ai.EventToken, Token: "before"},
		{Kind: ai.EventToolCall, ToolCall: &ai.ToolCall{ID: "x", Name: "bash"}},
		{Kind: ai.EventToken, Token: "after"},
		{Kind: ai.EventDone},
	}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	ch, _ := rt.ConverseStream(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	var kinds []runtime.EventKind
	var tokens []string
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == runtime.EventToken {
			tokens = append(tokens, ev.Token)
		}
	}
	for _, k := range kinds {
		if k != runtime.EventToken && k != runtime.EventDone {
			t.Fatalf("unexpected event kind in stream: %v (kinds=%v)", k, kinds)
		}
	}
	if join(tokens) != "beforeafter" {
		t.Fatalf("tokens: got %q want beforeafter", join(tokens))
	}
}

// TestConverseStream_ForwardsSubEvents (#340 R4j1) pins that observability
// sub-events surfaced by the Provider stack — thinking / tool_use /
// tool_input / tool_output — reach the consumer verbatim, translated to the
// runtime.Event kinds. This is what the pipeline needs to render progress
// without reaching into the Provider layer.
func TestConverseStream_ForwardsSubEvents(t *testing.T) {
	eng := &fakeEngine{scripts: [][]ai.Event{{
		{Kind: ai.EventThinking, Text: "pondering"},
		{Kind: ai.EventToolUse, ToolName: "grep"},
		{Kind: ai.EventToolInput, ToolName: "grep", Text: `{"p":"x"}`},
		{Kind: ai.EventToolOutput, ToolID: "call_1", Text: "match\n"},
		{Kind: ai.EventToken, Token: "final"},
		{Kind: ai.EventDone},
	}}}
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       eng,
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	ch, err := rt.ConverseStream(context.Background(), runtime.ConverseRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("ConverseStream: %v", err)
	}
	var got []runtime.Event
	for ev := range ch {
		got = append(got, ev)
	}
	// Expect: Thinking, ToolUse, ToolInput, ToolOutput, Token, Done (6 events).
	if len(got) != 6 {
		t.Fatalf("event count: got %d want 6 (%+v)", len(got), got)
	}
	if got[0].Kind != runtime.EventThinking || got[0].Text != "pondering" {
		t.Fatalf("thinking: %+v", got[0])
	}
	if got[1].Kind != runtime.EventToolUse || got[1].ToolName != "grep" {
		t.Fatalf("tool_use: %+v", got[1])
	}
	if got[2].Kind != runtime.EventToolInput || got[2].ToolName != "grep" || got[2].Text != `{"p":"x"}` {
		t.Fatalf("tool_input: %+v", got[2])
	}
	if got[3].Kind != runtime.EventToolOutput || got[3].ToolID != "call_1" || got[3].Text != "match\n" {
		t.Fatalf("tool_output: %+v", got[3])
	}
	if got[4].Kind != runtime.EventToken || got[4].Token != "final" {
		t.Fatalf("token: %+v", got[4])
	}
	if got[5].Kind != runtime.EventDone {
		t.Fatalf("done: %+v", got[5])
	}
}

// TestConverseStream_ValidationErrorsReturnSync proves the constructor-style
// path: invalid requests return synchronously from ConverseStream (no
// channel allocated) so callers get a plain error instead of having to
// drain a goroutine just to learn the Prompt was empty.
func TestConverseStream_ValidationErrorsReturnSync(t *testing.T) {
	rt, _ := runtime.New(runtime.Deps{
		Registry: newFakeRegistry(),
		Memory:   newFakeStore(),
		AI:       &fakeEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Model: "m"})

	if ch, err := rt.ConverseStream(context.Background(), runtime.ConverseRequest{}); err == nil || ch != nil {
		t.Fatal("expected (nil, error) for empty Prompt")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// errEngine always fails on Run.
type errEngine struct{ err error }

func (e *errEngine) Run(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	return nil, e.err
}

func join(parts []string) string {
	s := ""
	for _, p := range parts {
		s += p
	}
	return s
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
