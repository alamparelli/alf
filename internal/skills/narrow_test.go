package skills

import (
	"reflect"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

func TestNarrowToolsByDeclares_NilLookupReturnsTierTools(t *testing.T) {
	got := NarrowToolsByDeclares(nil, []string{"x"}, []string{"a", "b"})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("nil lookup must passthrough, got %v", got)
	}
}

func TestNarrowToolsByDeclares_EmptyActiveSkillsReturnsTierTools(t *testing.T) {
	lookup := func(string) []string { return []string{"a"} }
	got := NarrowToolsByDeclares(lookup, nil, []string{"a", "b"})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("no active skills must passthrough, got %v", got)
	}
}

func TestNarrowToolsByDeclares_EmptyTierToolsReturnsItself(t *testing.T) {
	lookup := func(string) []string { return []string{"a"} }
	got := NarrowToolsByDeclares(lookup, []string{"x"}, nil)
	if got != nil {
		t.Errorf("empty tier tools must passthrough nil, got %v", got)
	}
}

// TestNarrowToolsByDeclares_NarrowsToIntersection — the load-bearing
// invariant. A skill with declares = [a, c] active over a tier that
// offers [a, b, c, d] sees only [a, c]. The acceptance criterion from
// the #389 issue body.
func TestNarrowToolsByDeclares_NarrowsToIntersection(t *testing.T) {
	lookup := func(name string) []string {
		if name == "research" {
			return []string{"a", "c"}
		}
		return nil
	}
	got := NarrowToolsByDeclares(lookup, []string{"research"}, []string{"a", "b", "c", "d"})
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNarrowToolsByDeclares_PreservesTierToolsOrder(t *testing.T) {
	lookup := func(string) []string { return []string{"c", "a"} } // unsorted in declares
	got := NarrowToolsByDeclares(lookup, []string{"x"}, []string{"a", "b", "c"})
	want := []string{"a", "c"} // tier order
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order must follow tier, got %v want %v", got, want)
	}
}

func TestNarrowToolsByDeclares_UnionAcrossSkills(t *testing.T) {
	lookup := func(name string) []string {
		switch name {
		case "research":
			return []string{"a"}
		case "writer":
			return []string{"b"}
		}
		return nil
	}
	got := NarrowToolsByDeclares(lookup, []string{"research", "writer"}, []string{"a", "b", "c"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("union must include both, got %v want %v", got, want)
	}
}

func TestNarrowToolsByDeclares_YAMLOnlySkillsLeaveTierUnchanged(t *testing.T) {
	// Both active skills are YAML-only (no manifest.toml shipped yet).
	// During the §389 Stage 2 transition, the LLM still sees the full
	// tier surface. Once every active skill ships a manifest.toml the
	// narrow-or-empty rule kicks in via the next case.
	lookup := func(string) []string { return nil }
	got := NarrowToolsByDeclares(lookup, []string{"yaml-skill-a", "yaml-skill-b"}, []string{"a", "b"})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("YAML-only mix must passthrough, got %v", got)
	}
}

func TestNarrowToolsByDeclares_DeclaresOutsideTierAreIgnored(t *testing.T) {
	// A skill declaring "exotic-tool" while the tier doesn't enable it
	// must NOT magically grant access. Intersection semantics.
	lookup := func(string) []string { return []string{"a", "exotic-tool"} }
	got := NarrowToolsByDeclares(lookup, []string{"x"}, []string{"a", "b"})
	want := []string{"a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (declares cannot extend tier)", got, want)
	}
}

func TestNarrowToolsByDeclares_NarrowsToEmptyWhenNoOverlap(t *testing.T) {
	lookup := func(string) []string { return []string{"x", "y"} }
	got := NarrowToolsByDeclares(lookup, []string{"strict"}, []string{"a", "b"})
	if len(got) != 0 {
		t.Errorf("no overlap: got %v, want empty", got)
	}
}

func TestNarrowToolsByDeclares_MixedYAMLAndManifestSkills(t *testing.T) {
	// One active skill ships a manifest.toml with declares = [a]; the
	// other is YAML-only. Strict interpretation kicks in: at least one
	// declares block exists, so the LLM-visible surface is narrowed —
	// the YAML-only skill cannot widen back to the legacy "all tools"
	// path.
	lookup := func(name string) []string {
		if name == "manifested" {
			return []string{"a"}
		}
		return nil
	}
	got := NarrowToolsByDeclares(lookup, []string{"yaml-only", "manifested"}, []string{"a", "b", "c"})
	want := []string{"a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mixed YAML+manifest: got %v, want %v", got, want)
	}
}

func TestDeclaresFromVerified_Empty(t *testing.T) {
	if got := DeclaresFromVerified(nil); got != nil {
		t.Errorf("nil input: got %v, want nil", got)
	}
	if got := DeclaresFromVerified(&VerifiedSkill{}); got != nil {
		t.Errorf("nil manifest: got %v, want nil", got)
	}
	vs := &VerifiedSkill{
		Manifest: &envelope.Manifest{},
		Instance: &handle.Instance{},
	}
	if got := DeclaresFromVerified(vs); got != nil {
		t.Errorf("empty declares: got %v, want nil", got)
	}
}

func TestDeclaresFromVerified_FlattensIDs(t *testing.T) {
	vs := &VerifiedSkill{
		Manifest: &envelope.Manifest{
			Tools: envelope.ToolsBlock{
				Declares: []envelope.ToolDeclaration{
					{ID: "web-fetch"},
					{ID: "memory-recall"},
				},
			},
		},
	}
	got := DeclaresFromVerified(vs)
	want := []string{"web-fetch", "memory-recall"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
