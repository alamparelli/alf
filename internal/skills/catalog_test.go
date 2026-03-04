package skills

import (
	"strings"
	"testing"
)

func TestBuildCatalog_Empty(t *testing.T) {
	store := NewFileSkillStore("/nonexistent", "/nonexistent")
	result := BuildCatalog(store)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestBuildCatalog_Multiple(t *testing.T) {
	sysDir := t.TempDir()
	writeSkill(t, sysDir, "coding", `---
description: Expert coding
---
Prompt.
`)
	writeSkill(t, sysDir, "memory-expert", `---
description: Memory management
---
Prompt.
`)

	store := NewFileSkillStore(sysDir, "")
	result := BuildCatalog(store)

	if !strings.Contains(result, "=== [Available Skills] ===") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "- coding: Expert coding") {
		t.Error("missing coding skill")
	}
	if !strings.Contains(result, "- memory-expert: Memory management") {
		t.Error("missing memory-expert skill")
	}
	if !strings.Contains(result, "To use a skill, read its SKILL.md") {
		t.Error("missing footer instruction")
	}
}

func TestBuildCatalog_SkipsEmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "no-desc", "Just a body, no frontmatter.")

	store := NewFileSkillStore(dir, "")
	result := BuildCatalog(store)
	if result != "" {
		t.Errorf("expected empty catalog for skill without description, got %q", result)
	}
}
