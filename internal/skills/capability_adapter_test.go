package skills

import (
	"context"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
)

// memStore is a minimal in-memory Store for adapter tests.
type memStore struct {
	skills map[string]*Skill
}

func newMemStore(skills ...*Skill) *memStore {
	m := &memStore{skills: map[string]*Skill{}}
	for _, sk := range skills {
		m.skills[sk.Name] = sk
	}
	return m
}

func (m *memStore) All() []*Skill {
	out := make([]*Skill, 0, len(m.skills))
	for _, sk := range m.skills {
		out = append(out, sk)
	}
	return out
}
func (m *memStore) Get(name string) (*Skill, bool)             { sk, ok := m.skills[name]; return sk, ok }
func (m *memStore) Reload() error                                 { return nil }
func (m *memStore) AddDynamicTriggers(_ string, _ []string)      {}

func TestSkillCapability_Manifest(t *testing.T) {
	sk := &Skill{Name: "commit-push", Version: "1.0", Description: "commit + push"}
	c := asCapability(sk)
	m := c.Manifest()
	if m.ID != capability.ID("commit-push") {
		t.Fatalf("ID: got %q", m.ID)
	}
	if m.Kind != capability.KindSkill {
		t.Fatalf("Kind: got %v, want KindSkill", m.Kind)
	}
	if m.Name != "commit-push" || m.Version != "1.0" || m.Description != "commit + push" {
		t.Fatalf("Manifest: %+v", m)
	}
}

func TestSkillCapability_ExecuteReturnsPrompt(t *testing.T) {
	sk := &Skill{Name: "doc", Description: "d", Prompt: "the body"}
	out, err := asCapability(sk).Execute(context.Background(), capability.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := out.Data.(string); got != "the body" {
		t.Fatalf("Execute Data: got %q", got)
	}
}

func TestMirrorInto_RegistersAllDescribed(t *testing.T) {
	store := newMemStore(
		&Skill{Name: "a", Description: "da", Prompt: "pa"},
		&Skill{Name: "b", Description: "db", Prompt: "pb"},
	)
	reg := capability.NewRegistry()
	if err := MirrorInto(store, reg); err != nil {
		t.Fatalf("MirrorInto: %v", err)
	}
	if reg.Len() != 2 {
		t.Fatalf("Len: want 2, got %d", reg.Len())
	}
	skills := reg.ByKind(capability.KindSkill)
	if len(skills) != 2 {
		t.Fatalf("ByKind(Skill): want 2, got %d", len(skills))
	}
}

func TestMirrorInto_SkipsUndescribedSkills(t *testing.T) {
	store := newMemStore(
		&Skill{Name: "hidden", Description: ""},
		&Skill{Name: "visible", Description: "yes"},
	)
	reg := capability.NewRegistry()
	if err := MirrorInto(store, reg); err != nil {
		t.Fatalf("MirrorInto: %v", err)
	}
	if _, ok := reg.Get("hidden"); ok {
		t.Error("undescribed skill should be skipped")
	}
	if _, ok := reg.Get("visible"); !ok {
		t.Error("described skill should be mirrored")
	}
}

func TestMirrorInto_Idempotent(t *testing.T) {
	store := newMemStore(&Skill{Name: "s", Description: "d", Prompt: "v1"})
	reg := capability.NewRegistry()
	if err := MirrorInto(store, reg); err != nil {
		t.Fatalf("first MirrorInto: %v", err)
	}
	// Simulate skill body update on disk.
	store.skills["s"].Prompt = "v2"
	if err := MirrorInto(store, reg); err != nil {
		t.Fatalf("second MirrorInto: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("Len after re-mirror: want 1, got %d", reg.Len())
	}
	c, _ := reg.Get("s")
	out, _ := c.Execute(context.Background(), capability.Input{})
	if got, _ := out.Data.(string); got != "v2" {
		t.Fatalf("Replace should pick up new prompt; got %q", got)
	}
}

func TestMirrorInto_NilSafe(t *testing.T) {
	reg := capability.NewRegistry()
	if err := MirrorInto(nil, reg); err != nil {
		t.Fatalf("nil store: %v", err)
	}
	if err := MirrorInto(newMemStore(), nil); err != nil {
		t.Fatalf("nil reg: %v", err)
	}
}
