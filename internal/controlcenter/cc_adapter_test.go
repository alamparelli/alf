package controlcenter

import (
	"testing"

	"github.com/alamparelli/alf/internal/runtime"
)

func TestCCAdapter_Channel(t *testing.T) {
	a := newCCAdapter()
	if got := a.Channel(); got != "cc" {
		t.Errorf("Channel() = %q, want %q", got, "cc")
	}
}

func TestCCAdapter_OnEvent_ForwardsToCallback(t *testing.T) {
	a := newCCAdapter()
	var received []ChatEvent
	a.setCallback(func(evt ChatEvent) {
		received = append(received, evt)
	})

	a.OnEvent("cc:default", runtime.OutEvent{
		Type: "thinking",
		Data: map[string]string{"text": "hmm"},
	})
	a.OnEvent("cc:default", runtime.OutEvent{
		Type: "text_delta",
		Data: map[string]string{"text": "hello"},
	})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
	if received[0].Type != "thinking" {
		t.Errorf("event[0].Type = %q, want %q", received[0].Type, "thinking")
	}
	if received[1].Type != "text_delta" {
		t.Errorf("event[1].Type = %q, want %q", received[1].Type, "text_delta")
	}
}

func TestCCAdapter_OnEvent_SuppressesDone(t *testing.T) {
	a := newCCAdapter()
	var received []ChatEvent
	a.setCallback(func(evt ChatEvent) {
		received = append(received, evt)
	})

	a.OnEvent("cc:default", runtime.OutEvent{
		Type: "text",
		Data: map[string]string{"text": "hello"},
	})
	a.OnEvent("cc:default", runtime.OutEvent{
		Type: "done",
		Data: map[string]string{"model": "test"},
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 event (done suppressed), got %d", len(received))
	}
	if received[0].Type != "text" {
		t.Errorf("expected 'text' event, got %q", received[0].Type)
	}
}

func TestCCAdapter_OnEvent_NilCallback(t *testing.T) {
	a := newCCAdapter()
	// Should not panic with nil callback.
	a.OnEvent("cc:default", runtime.OutEvent{
		Type: "text",
		Data: map[string]string{"text": "hello"},
	})
}

func TestCCAdapter_SetCallback_Clears(t *testing.T) {
	a := newCCAdapter()
	called := false
	a.setCallback(func(evt ChatEvent) {
		called = true
	})
	a.setCallback(nil)

	a.OnEvent("cc:default", runtime.OutEvent{Type: "text", Data: map[string]string{}})
	if called {
		t.Error("expected callback to not be called after clearing")
	}
}

func TestOutEventToChatEvent(t *testing.T) {
	event := runtime.OutEvent{
		Type: "routed",
		Data: map[string]string{"tier": "fast", "model": "haiku"},
	}
	ce := outEventToChatEvent(event)
	if ce.Type != "routed" {
		t.Errorf("Type = %q, want %q", ce.Type, "routed")
	}
	data, ok := ce.Data.(map[string]string)
	if !ok {
		t.Fatal("expected Data to be map[string]string")
	}
	if data["tier"] != "fast" {
		t.Errorf("Data[tier] = %q, want %q", data["tier"], "fast")
	}
}
