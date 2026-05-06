package main

import (
	"reflect"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/skills"
)

func TestSkillsRuntime_DeclaresLookup_NilReceiver(t *testing.T) {
	var s *skillsRuntime
	if got := s.DeclaresLookup("anything"); got != nil {
		t.Errorf("nil receiver: got %v, want nil", got)
	}
}

func TestSkillsRuntime_DeclaresLookup_EmptyVerified(t *testing.T) {
	s := &skillsRuntime{}
	if got := s.DeclaresLookup("foo"); got != nil {
		t.Errorf("empty verified: got %v, want nil", got)
	}
}

func TestSkillsRuntime_DeclaresLookup_HitMatchesByName(t *testing.T) {
	s := &skillsRuntime{
		verified: []*skills.VerifiedSkill{
			{
				Skill: &skills.Skill{Name: "research"},
				Manifest: &envelope.Manifest{
					Tools: envelope.ToolsBlock{
						Declares: []envelope.ToolDeclaration{
							{ID: "web-fetch"},
							{ID: "memory-recall"},
						},
					},
				},
			},
			{
				Skill: &skills.Skill{Name: "writer"},
				Manifest: &envelope.Manifest{
					Tools: envelope.ToolsBlock{
						Declares: []envelope.ToolDeclaration{
							{ID: "memory-write"},
						},
					},
				},
			},
		},
	}

	got := s.DeclaresLookup("research")
	want := []string{"web-fetch", "memory-recall"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("research: got %v, want %v", got, want)
	}

	got = s.DeclaresLookup("writer")
	want = []string{"memory-write"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("writer: got %v, want %v", got, want)
	}

	if got := s.DeclaresLookup("ghost"); got != nil {
		t.Errorf("unknown name: got %v, want nil", got)
	}
}

func TestSkillsRuntime_DeclaresLookup_SkipsMalformedEntries(t *testing.T) {
	// nil entry + entry with nil Skill must not panic; the lookup
	// must keep walking to find the legitimate match.
	s := &skillsRuntime{
		verified: []*skills.VerifiedSkill{
			nil,
			{Skill: nil, Manifest: &envelope.Manifest{}},
			{
				Skill: &skills.Skill{Name: "research"},
				Manifest: &envelope.Manifest{
					Tools: envelope.ToolsBlock{
						Declares: []envelope.ToolDeclaration{{ID: "web-fetch"}},
					},
				},
			},
		},
	}
	got := s.DeclaresLookup("research")
	want := []string{"web-fetch"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSkillsRuntime_DeclaresLookup_VerifiedSliceMutationVisible(t *testing.T) {
	// The lookup walks s.verified each call (no cached map). Mutating
	// the slice in-place — same kind of swap Replace performs without
	// the Close() cascade — must surface immediately. Constructing
	// handle.Instances with valid lifecycles to exercise Replace itself
	// would require the full Instantiator + RuntimeToken — out of scope
	// for this unit test; the in-place mutation captures the same
	// "lookup is dynamic" property.
	s := &skillsRuntime{
		verified: []*skills.VerifiedSkill{
			{
				Skill:    &skills.Skill{Name: "old"},
				Manifest: &envelope.Manifest{Tools: envelope.ToolsBlock{Declares: []envelope.ToolDeclaration{{ID: "old-tool"}}}},
			},
		},
	}
	got := s.DeclaresLookup("old")
	if !reflect.DeepEqual(got, []string{"old-tool"}) {
		t.Fatalf("pre-swap: got %v, want [old-tool]", got)
	}

	s.verified = []*skills.VerifiedSkill{
		{
			Skill:    &skills.Skill{Name: "new"},
			Manifest: &envelope.Manifest{Tools: envelope.ToolsBlock{Declares: []envelope.ToolDeclaration{{ID: "new-tool"}}}},
		},
	}

	if got := s.DeclaresLookup("old"); got != nil {
		t.Errorf("after swap, old skill should be gone, got %v", got)
	}
	got = s.DeclaresLookup("new")
	if !reflect.DeepEqual(got, []string{"new-tool"}) {
		t.Errorf("after swap, new skill: got %v, want [new-tool]", got)
	}
}
