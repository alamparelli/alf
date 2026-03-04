package skills

import (
	"strings"
	"testing"
)

func TestBuildInjectionBlock_Empty(t *testing.T) {
	store := NewFileSkillStore("/nonexistent", "/nonexistent")
	result := BuildInjectionBlock(store, nil)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
	result = BuildInjectionBlock(store, []string{})
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestBuildInjectionBlock_Named(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "coding", `---
description: Expert coding
---

Be a great coder.
`)

	store := NewFileSkillStore(dir, "")
	result := BuildInjectionBlock(store, []string{"coding"})
	if !strings.Contains(result, "--- coding ---") {
		t.Error("missing skill header")
	}
	if !strings.Contains(result, "Be a great coder.") {
		t.Error("missing skill prompt")
	}
}

func TestBuildInjectionBlock_UnknownSkip(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "coding", `---
description: Expert coding
---

Be a great coder.
`)

	store := NewFileSkillStore(dir, "")
	result := BuildInjectionBlock(store, []string{"unknown", "coding"})
	if !strings.Contains(result, "--- coding ---") {
		t.Error("known skill should be included")
	}
	if strings.Contains(result, "unknown") {
		t.Error("unknown skill should be skipped")
	}
}
