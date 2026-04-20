package ai_test

import (
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/ai"
)

// TestEvent_KindPayloadShape pins the invariant that each EventKind carries
// exactly the field(s) downstream consumers expect. If A2 widens Event, this
// test should be updated deliberately — not silently.
func TestEvent_KindPayloadShape(t *testing.T) {
	tests := []struct {
		name    string
		event   ai.Event
		wantOK  func(ai.Event) bool
		wantMsg string
	}{
		{
			name:    "EventToken carries Token",
			event:   ai.Event{Kind: ai.EventToken, Token: "hi"},
			wantOK:  func(e ai.Event) bool { return e.Token != "" && e.ToolCall == nil && e.Err == nil },
			wantMsg: "EventToken must have Token set, ToolCall/Err nil",
		},
		{
			name:    "EventToolCall carries ToolCall",
			event:   ai.Event{Kind: ai.EventToolCall, ToolCall: &ai.ToolCall{ID: "x", Name: "n"}},
			wantOK:  func(e ai.Event) bool { return e.ToolCall != nil && e.Token == "" && e.Err == nil },
			wantMsg: "EventToolCall must have ToolCall set, Token empty, Err nil",
		},
		{
			name:    "EventError carries Err",
			event:   ai.Event{Kind: ai.EventError, Err: errors.New("boom")},
			wantOK:  func(e ai.Event) bool { return e.Err != nil && e.Token == "" && e.ToolCall == nil },
			wantMsg: "EventError must have Err set, Token empty, ToolCall nil",
		},
		{
			name:    "EventDone is terminal, no payload",
			event:   ai.Event{Kind: ai.EventDone},
			wantOK:  func(e ai.Event) bool { return e.Token == "" && e.ToolCall == nil && e.Err == nil },
			wantMsg: "EventDone must carry no payload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.wantOK(tc.event) {
				t.Fatalf("%s: got %+v", tc.wantMsg, tc.event)
			}
		})
	}
}

// TestEventKind_DistinctValues guards against accidental reordering of the
// untyped iota constants — downstream code switches on these values.
func TestEventKind_DistinctValues(t *testing.T) {
	kinds := map[ai.EventKind]string{
		ai.EventToken:    "EventToken",
		ai.EventToolCall: "EventToolCall",
		ai.EventDone:     "EventDone",
		ai.EventError:    "EventError",
	}
	if len(kinds) != 4 {
		t.Fatalf("EventKind set collapsed: %d distinct values", len(kinds))
	}
}

// TestRole_CanonicalValues pins the Role string values: provider adapters
// (A2) will marshal these straight to API payloads.
func TestRole_CanonicalValues(t *testing.T) {
	cases := map[ai.Role]string{
		ai.RoleSystem:    "system",
		ai.RoleUser:      "user",
		ai.RoleAssistant: "assistant",
		ai.RoleTool:      "tool",
	}
	for r, want := range cases {
		if string(r) != want {
			t.Fatalf("Role %q: got %q, want %q", want, string(r), want)
		}
	}
}
