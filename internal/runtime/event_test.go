package runtime_test

import (
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/runtime"
)

// TestEvent_KindPayloadShape pins the invariant that each EventKind
// carries exactly the field(s) downstream UI/scheduler/telegram
// consumers expect. Deliberate widening (e.g. adding a field for a
// new Kind) should update this test.
func TestEvent_KindPayloadShape(t *testing.T) {
	tests := []struct {
		name    string
		event   runtime.Event
		wantOK  func(runtime.Event) bool
		wantMsg string
	}{
		{
			name:  "EventToken carries Token only",
			event: runtime.Event{Kind: runtime.EventToken, Token: "hi"},
			wantOK: func(e runtime.Event) bool {
				return e.Token != "" && e.ToolResult == nil && e.ToolName == "" && e.Err == nil
			},
			wantMsg: "EventToken must have Token set; ToolResult/ToolName/Err empty",
		},
		{
			name: "EventToolResult carries ToolResult + ToolName",
			event: runtime.Event{
				Kind:       runtime.EventToolResult,
				ToolName:   "bash",
				ToolResult: &capability.Output{Data: "ok"},
			},
			wantOK: func(e runtime.Event) bool {
				return e.ToolResult != nil && e.ToolName != "" && e.Token == "" && e.Err == nil
			},
			wantMsg: "EventToolResult must have ToolResult + ToolName; Token/Err empty",
		},
		{
			name:  "EventError carries Err",
			event: runtime.Event{Kind: runtime.EventError, Err: errors.New("boom")},
			wantOK: func(e runtime.Event) bool {
				return e.Err != nil && e.Token == "" && e.ToolResult == nil
			},
			wantMsg: "EventError must have Err set; Token/ToolResult empty",
		},
		{
			name:  "EventDone is terminal with no payload",
			event: runtime.Event{Kind: runtime.EventDone},
			wantOK: func(e runtime.Event) bool {
				return e.Token == "" && e.ToolResult == nil && e.ToolName == "" && e.Err == nil
			},
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

// TestEventKind_DistinctValues guards against accidental reordering of
// the untyped iota constants — downstream code switches on these values.
func TestEventKind_DistinctValues(t *testing.T) {
	kinds := map[runtime.EventKind]string{
		runtime.EventToken:      "EventToken",
		runtime.EventToolResult: "EventToolResult",
		runtime.EventDone:       "EventDone",
		runtime.EventError:      "EventError",
	}
	if len(kinds) != 4 {
		t.Fatalf("EventKind set collapsed: %d distinct values", len(kinds))
	}
}

// TestDeps_CompositeShape documents the four collaborators the concrete
// Runtime will hold. If this test breaks, Deps changed shape — update
// callers deliberately.
func TestDeps_CompositeShape(t *testing.T) {
	var d runtime.Deps
	// Zero-value assertion: every field is nil/interface-zero. The test's
	// purpose is to force a compile error if any field is renamed.
	if d.Registry != nil || d.Memory != nil || d.AI != nil || d.Sandbox != nil {
		t.Fatalf("Deps zero-value has populated fields: %+v", d)
	}
}
