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

// TestEnforceTier2Ceiling_RejectsCapabilityProviderKind pins
// SEC-080-006: a capability-provider manifest registers new
// handle kinds in the runtime registry under the signer's
// fingerprint. The local daemon key cannot pre-approve that
// trust-surface widening — the user-endorsed key (Tier 3) is the
// only path. Today the daemon-key bootstrap auto-signs anything
// in <skillsDir>/wasm/; without this gate, an LLM that drops a
// capability-provider manifest would silently widen the registry
// once forge wiring for [[depends]] consumption lands (Stage 5+).
func TestEnforceTier2Ceiling_RejectsCapabilityProviderKind(t *testing.T) {
	m := &Manifest{Kind: KindCapabilityProvider}
	err := EnforceTier2Ceiling(m)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded for capability-provider kind", err)
	}
}

// TestEnforceTier2Ceiling_AcceptsNonProviderKinds pins that
// SEC-080-006's kind gate refuses ONLY capability-provider —
// every other recognised kind is within ceiling.
func TestEnforceTier2Ceiling_AcceptsNonProviderKinds(t *testing.T) {
	for _, k := range []ManifestKind{
		KindWASMTool, KindWASMApp, KindSkill, KindLLMProvider, KindMarketplaceApp,
	} {
		m := &Manifest{Kind: k}
		if err := EnforceTier2Ceiling(m); err != nil {
			t.Errorf("kind=%q rejected: %v", k, err)
		}
	}
}

// TestEnforceTier2Ceiling_AcceptsAlfNamespaceDepends pins that
// depends on alf: core kinds (fs, http, exec, secrets, events.pub,
// events.sub, tool) stay within ceiling — they reference handles
// the daemon owns end-to-end, not authority pulled from another
// publisher.
func TestEnforceTier2Ceiling_AcceptsAlfNamespaceDepends(t *testing.T) {
	m := &Manifest{
		Depends: []DependsEntry{
			{Handle: "alf:fs"},
			{Handle: "alf:tool"},
		},
	}
	if err := EnforceTier2Ceiling(m); err != nil {
		t.Errorf("alf: namespace depends rejected: %v", err)
	}
}

// TestEnforceTier2Ceiling_RejectsCrossPublisherDepends pins
// SEC-080-006: a manifest that declares [[depends]] on a non-alf
// namespace (publisher fingerprint short) is pulling authority
// from another publisher's exported handle. The daemon key
// cannot pre-approve cross-publisher trust dependence; the
// user-endorsed key (Tier 3) is the right signer.
func TestEnforceTier2Ceiling_RejectsCrossPublisherDepends(t *testing.T) {
	m := &Manifest{
		Depends: []DependsEntry{
			{Handle: "deadbeefdeadbeef:bluetooth.scan"},
		},
	}
	err := EnforceTier2Ceiling(m)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded for cross-publisher depends", err)
	}
}

// TestEnforceTier2Ceiling_RejectsRawImports pins SEC-080-006:
// even allowlisted [[raw_imports]] (wasi:clocks/*, wasi:cli/*)
// are not ambient defaults. The Tier-2 bootstrap is "LLM authors
// a tool with the ambient defaults"; raw imports widen the WASI
// surface and need explicit operator review via Tier 3.
func TestEnforceTier2Ceiling_RejectsRawImports(t *testing.T) {
	m := &Manifest{
		RawImports: []RawImport{
			{Module: "wasi:clocks/monotonic-clock", Function: "now", Justification: "perf timings"},
		},
	}
	err := EnforceTier2Ceiling(m)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded for raw_imports", err)
	}
}

// TestEnforceTier2Ceiling_RejectsHTTPScopes pins #421 Wave 1's
// load-bearing ceiling rule: [[http.scopes]] widens the trust
// surface (outbound HTTP to an explicit allowlist), and the
// local daemon key cannot pre-approve that. Only the user-endorsed
// key (Tier 3) signs manifests that declare http scopes; the
// signer/loader refuses Tier 2 attempts with ErrCeilingExceeded.
func TestEnforceTier2Ceiling_RejectsHTTPScopes(t *testing.T) {
	m := &Manifest{
		HTTP: HTTPBlock{
			Scopes: []HTTPScope{
				{Host: "openlibrary.org"},
			},
		},
	}
	err := EnforceTier2Ceiling(m)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded for http.scopes", err)
	}
}
