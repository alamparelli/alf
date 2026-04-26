package envelope

import (
	"errors"
	"testing"
)

// TestEnforceTier2Ceiling_AcceptsBareManifest pins that the bare
// minimum manifest (id/kind/version/name only, no surfaces) passes
// the Tier-2 ceiling. The local daemon key is allowed to sign these
// without operator intervention.
func TestEnforceTier2Ceiling_AcceptsBareManifest(t *testing.T) {
	m := &Manifest{}
	if err := EnforceTier2Ceiling(m); err != nil {
		t.Errorf("bare manifest rejected: %v", err)
	}
}

// TestEnforceTier2Ceiling_AcceptsFSReadsAndWrites pins that
// fs paths inside the bundle's own dir are within the
// "fs: own-dir" ceiling. Schema validation already rejects absolute
// paths and ".." segments, so any path that reaches this function
// is by construction relative to the bundle root.
func TestEnforceTier2Ceiling_AcceptsFSReadsAndWrites(t *testing.T) {
	m := &Manifest{
		FS: FSBlock{
			Reads:  []FSPath{{Path: "data/"}, {Path: "config.toml"}},
			Writes: []FSPath{{Path: "output/"}},
		},
	}
	if err := EnforceTier2Ceiling(m); err != nil {
		t.Errorf("own-dir fs scopes rejected: %v", err)
	}
}

// TestEnforceTier2Ceiling_AcceptsOwnTopicExports pins that a
// publisher exporting its own topics passes. "events: own-topics"
// in §7.3 means the cap can publish on topics it declares — that's
// inside the ceiling.
func TestEnforceTier2Ceiling_AcceptsOwnTopicExports(t *testing.T) {
	m := &Manifest{
		Events: EventsBlock{
			Exports: []EventExport{{Topic: "chat.log"}, {Topic: "task.done"}},
		},
	}
	if err := EnforceTier2Ceiling(m); err != nil {
		t.Errorf("own-topic exports rejected: %v", err)
	}
}

// TestEnforceTier2Ceiling_RejectsCrossFlowSubscriptions pins
// SEC-004's load-bearing rule: a manifest that subscribes to
// another cap's events is requesting cross-cap authority, which
// the local daemon key cannot pre-approve. The user must re-sign
// with the user-endorsed key (Tier 3) via `alf keygen`.
func TestEnforceTier2Ceiling_RejectsCrossFlowSubscriptions(t *testing.T) {
	m := &Manifest{
		Events: EventsBlock{
			Subscribes: []EventSubscription{{From: "pub-cap", Topic: "chat.log"}},
		},
	}
	err := EnforceTier2Ceiling(m)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded", err)
	}
}

// TestEnforceTier2Ceiling_AcceptsToolDeclares pins that
// [[tools.declares]] entries pass — the forge gates the runtime
// authority via ToolHandle scope, so signing is fine. This is
// distinct from the §3.1 active-skill boundary (which lives at the
// LLM tool-spec layer, not at signing).
func TestEnforceTier2Ceiling_AcceptsToolDeclares(t *testing.T) {
	m := &Manifest{
		Tools: ToolsBlock{
			Declares: []ToolDeclaration{{ID: "calc"}, {ID: "echo"}},
		},
	}
	if err := EnforceTier2Ceiling(m); err != nil {
		t.Errorf("tools.declares rejected: %v", err)
	}
}

// TestEnforceTier2Ceiling_RejectsNilManifest pins that calling the
// ceiling with a nil manifest is a programming error — surfaces as
// ErrCeilingExceeded so signers fail safely rather than silently
// signing a nil manifest.
func TestEnforceTier2Ceiling_RejectsNilManifest(t *testing.T) {
	err := EnforceTier2Ceiling(nil)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Errorf("got %v, want ErrCeilingExceeded for nil manifest", err)
	}
}
