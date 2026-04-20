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
		{"missing Model", func(_ *runtime.Deps, o *runtime.Options) { o.Model = "" }},
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

	ch, err := rt.Chat(context.Background(), "conv-1", "hi")
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

	ch, err := rt.Chat(context.Background(), "conv-1", "please echo")
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

	ch, _ := rt.Chat(context.Background(), "c", "x")
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

	ch, err := rt.Chat(context.Background(), "c", "x")
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
	ch, _ := rt.Chat(context.Background(), "c", "x")
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
	ch, _ := rt.Chat(context.Background(), "c", "x")
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
	if _, err := rt.Chat(context.Background(), "", "x"); err == nil {
		t.Fatal("expected error for empty convID")
	}
	if _, err := rt.Chat(context.Background(), "c", ""); err == nil {
		t.Fatal("expected error for empty userInput")
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
