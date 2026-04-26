package runtime

import (
	"testing"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
)

// fixtureManifests returns three manifests covering common shapes.
// Used by the BuildScopedToolSpecs test cases.
func fixtureManifests() []capability.Manifest {
	return []capability.Manifest{
		{ID: "read-file", Description: "Read a file"},
		{ID: "bash", Description: "Run a bash command"},
		{ID: "write-file", Description: "Write a file"},
	}
}

func TestBuildScopedToolSpecs_AllowlistFilters(t *testing.T) {
	specs := BuildScopedToolSpecs(fixtureManifests(), []capability.ID{"bash"})
	if len(specs) != 1 {
		t.Fatalf("len=%d, want 1", len(specs))
	}
	if specs[0].Name != "bash" {
		t.Errorf("Name=%q, want bash", specs[0].Name)
	}
}

func TestBuildScopedToolSpecs_TwoAllowedReturnsBoth(t *testing.T) {
	specs := BuildScopedToolSpecs(fixtureManifests(), []capability.ID{"bash", "read-file"})
	if len(specs) != 2 {
		t.Fatalf("len=%d, want 2", len(specs))
	}
	names := []string{specs[0].Name, specs[1].Name}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["bash"] || !got["read-file"] {
		t.Errorf("expected bash + read-file, got %v", names)
	}
}

// TestBuildScopedToolSpecs_NilAllowedYieldsAll pins the SEC-005
// legacy-compatibility semantic: a nil allowlist means "no boundary
// configured for this turn — surface every registered manifest".
// This is the path for callers that have not yet wired an active-skill
// boundary (most Chat callers as of v0.8.0-beta). The orchestrator
// post-#389 Stage 2 will pass a non-nil allowlist, which narrows the
// surface to the active skill's [[tools.declares]].
//
// Distinct from the empty-slice case: nil = "no boundary",
// []capability.ID{} = "explicit empty boundary, surface zero tools".
func TestBuildScopedToolSpecs_NilAllowedYieldsAll(t *testing.T) {
	specs := BuildScopedToolSpecs(fixtureManifests(), nil)
	if len(specs) != 3 {
		t.Fatalf("nil allowed: want 3 specs (all manifests), got %d: %v", len(specs), specs)
	}
}

// TestBuildScopedToolSpecs_EmptyAllowedYieldsEmpty pins that an
// explicit empty allowlist (non-nil, length zero) surfaces zero
// tools — distinct from nil. This is the "active skill declared no
// tools" path.
func TestBuildScopedToolSpecs_EmptyAllowedYieldsEmpty(t *testing.T) {
	specs := BuildScopedToolSpecs(fixtureManifests(), []capability.ID{})
	if specs != nil {
		t.Errorf("empty allowed: want nil specs, got %v", specs)
	}
}

func TestBuildScopedToolSpecs_UndeclaredIDsAbsent(t *testing.T) {
	// allowlist references a tool that does not exist in the registry —
	// it must not appear in the output (no error, just absent).
	specs := BuildScopedToolSpecs(fixtureManifests(), []capability.ID{"bash", "imaginary-tool"})
	if len(specs) != 1 || specs[0].Name != "bash" {
		t.Errorf("specs=%v, want only bash", specs)
	}
}

func TestBuildScopedToolSpecs_EmptyRegistryYieldsEmpty(t *testing.T) {
	specs := BuildScopedToolSpecs(nil, []capability.ID{"bash"})
	if specs != nil {
		t.Errorf("nil registry: want nil specs, got %v", specs)
	}
}

func TestBuildScopedToolSpecs_DescriptionPreserved(t *testing.T) {
	specs := BuildScopedToolSpecs(fixtureManifests(), []capability.ID{"read-file"})
	if len(specs) != 1 {
		t.Fatalf("len=%d", len(specs))
	}
	if specs[0].Description != "Read a file" {
		t.Errorf("Description=%q", specs[0].Description)
	}
	// Sanity: ToolSpec is the AI-facing type.
	var _ ai.ToolSpec = specs[0]
}
