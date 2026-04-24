package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/platform/session"
)

// stubAdapter records events and calls for engine-level tests.
type stubAdapter struct {
	mu       sync.Mutex
	name     string
	sent     []string
	events   []OutEvent
	sendErr  error
}

func (s *stubAdapter) Channel() string { return s.name }
func (s *stubAdapter) SendText(_ ChannelID, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, text)
	return "msg-id", s.sendErr
}
func (s *stubAdapter) SendReaction(_ ChannelID, _ string, _ string) error { return nil }
func (s *stubAdapter) OnEvent(_ ChannelID, event OutEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func TestNewEngine_CopiesConfig(t *testing.T) {
	cfg := EngineConfig{
		DataDir:                "/data",
		ConfigDir:              "/config",
		ContextDir:             "/ctx",
		Sessions:               session.New(t.TempDir(), time.Minute),
		SummarizationEnabled:   true,
		SummarizationThreshold: 50,
		SummarizationKeepLast:  5,
	}
	e := NewEngine(cfg)

	if e.DataDir != "/data" || e.ConfigDir != "/config" || e.ContextDir != "/ctx" {
		t.Errorf("directory fields not copied: %+v", e)
	}
	if !e.SummarizationEnabled || e.SummarizationThreshold != 50 || e.SummarizationKeepLast != 5 {
		t.Errorf("summarization config not copied: %+v", e)
	}
	if e.adapters == nil {
		t.Error("adapters map must be initialised")
	}
}

func TestEngine_RegisterAndLookupAdapter(t *testing.T) {
	e := NewEngine(EngineConfig{})
	tg := &stubAdapter{name: "tg"}
	cc := &stubAdapter{name: "cc"}

	e.RegisterAdapter(tg)
	e.RegisterAdapter(cc)

	if got := e.Adapter("tg"); got != tg {
		t.Errorf("Adapter(tg) mismatch: %v", got)
	}
	if got := e.Adapter("cc"); got != cc {
		t.Errorf("Adapter(cc) mismatch: %v", got)
	}
	if got := e.Adapter("missing"); got != nil {
		t.Errorf("unknown adapter must be nil, got %v", got)
	}
}

func TestEngine_Emit_DeliversToRegisteredAdapter(t *testing.T) {
	e := NewEngine(EngineConfig{})
	tg := &stubAdapter{name: "tg"}
	e.RegisterAdapter(tg)

	e.emit(ChannelID("tg:123"), OutEvent{Type: "text", Data: map[string]string{"text": "hi"}})

	if len(tg.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(tg.events))
	}
	if tg.events[0].Type != "text" || tg.events[0].Data["text"] != "hi" {
		t.Errorf("event mismatch: %+v", tg.events[0])
	}
}

func TestEngine_Emit_MissingAdapterIsNoOp(t *testing.T) {
	e := NewEngine(EngineConfig{})
	// Must not panic when no adapter is registered for this channel.
	e.emit(ChannelID("tg:xyz"), OutEvent{Type: "text"})
}

func TestEngine_Broadcast_AllAdapters(t *testing.T) {
	BroadcastChannel = "all"
	defer func() { BroadcastChannel = "" }()

	e := NewEngine(EngineConfig{})
	tg := &stubAdapter{name: "tg"}
	cc := &stubAdapter{name: "cc"}
	e.RegisterAdapter(tg)
	e.RegisterAdapter(cc)

	e.Broadcast("system alert")

	if len(tg.sent) != 1 || tg.sent[0] != "system alert" {
		t.Errorf("tg did not receive broadcast: %+v", tg.sent)
	}
	if len(cc.sent) != 1 || cc.sent[0] != "system alert" {
		t.Errorf("cc did not receive broadcast: %+v", cc.sent)
	}
}

func TestEngine_Broadcast_Filtered(t *testing.T) {
	BroadcastChannel = "cc"
	defer func() { BroadcastChannel = "" }()

	e := NewEngine(EngineConfig{})
	tg := &stubAdapter{name: "tg"}
	cc := &stubAdapter{name: "cc"}
	e.RegisterAdapter(tg)
	e.RegisterAdapter(cc)

	e.Broadcast("cc only")

	if len(tg.sent) != 0 {
		t.Errorf("tg must not receive filtered broadcast: %+v", tg.sent)
	}
	if len(cc.sent) != 1 {
		t.Errorf("cc must receive the broadcast: %+v", cc.sent)
	}
}

func TestEngine_SignalEnv(t *testing.T) {
	e := &ChatEngine{}
	// Empty sock path → env unchanged.
	got := e.signalEnv([]string{"FOO=1"})
	if len(got) != 1 {
		t.Errorf("expected unchanged env, got %v", got)
	}

	e.SignalSockPath = "/tmp/alf.sock"
	got = e.signalEnv([]string{"FOO=1"})
	if len(got) != 2 {
		t.Fatalf("expected ALF_SIGNAL_SOCK appended, got %v", got)
	}
	if got[1] != "ALF_SIGNAL_SOCK=/tmp/alf.sock" {
		t.Errorf("unexpected appended var: %q", got[1])
	}

	// Existing ALF_SIGNAL_SOCK must be preserved (per-request override wins).
	got = e.signalEnv([]string{"FOO=1", "ALF_SIGNAL_SOCK=/per/req.sock"})
	if len(got) != 2 {
		t.Errorf("expected unchanged env when ALF_SIGNAL_SOCK already present, got %v", got)
	}
	if got[1] != "ALF_SIGNAL_SOCK=/per/req.sock" {
		t.Errorf("existing socket must not be overridden, got %q", got[1])
	}
}

func TestNewConversation_NilStore(t *testing.T) {
	if got := newConversation(nil, "tg"); got != "" {
		t.Errorf("nil store must return empty, got %q", got)
	}
}

func TestNewConversation_RotatesAndReturnsID(t *testing.T) {
	store := memory.NewInMem()
	ctx := context.Background()
	// Seed an initial active-conv pref so rotation has an "old" value.
	_ = store.SetPref(ctx, "active_conv:tg", "conv-seed")
	first, _ := store.GetPref(ctx, "active_conv:tg")
	firstStr, _ := first.(string)

	got := newConversation(store, "tg")
	if got == "" {
		t.Fatal("expected non-empty conv id")
	}
	if got == firstStr {
		t.Errorf("expected rotated id, got same as before: %q", got)
	}
	cur, _ := store.GetPref(ctx, "active_conv:tg")
	if s, _ := cur.(string); s != got {
		t.Errorf("active_conv pref should match the returned value; got %v want %q", s, got)
	}
}
