package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, dir, name, filename, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	os.MkdirAll(skillDir, 0o755)
	if err := os.WriteFile(filepath.Join(skillDir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "coding", `---
name: coding
description: Expert software engineering
version: "1"
---

You are an expert coder.
`)

	sk, err := parseSkill(filepath.Join(dir, "coding"))
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "coding" {
		t.Errorf("name = %q, want coding", sk.Name)
	}
	if sk.Description != "Expert software engineering" {
		t.Errorf("description = %q", sk.Description)
	}
	if sk.Version != "1" {
		t.Errorf("version = %q", sk.Version)
	}
	if sk.Prompt != "You are an expert coder." {
		t.Errorf("prompt = %q", sk.Prompt)
	}
}

func TestParseSkill_WithRefs(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "coding", `---
description: Expert coding
---

Main instructions.
`)
	writeFile(t, dir, "coding", "patterns.md", "Pattern reference content")

	sk, err := parseSkill(filepath.Join(dir, "coding"))
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "coding" {
		t.Errorf("name = %q, want coding (dirname fallback)", sk.Name)
	}
	if sk.Prompt != "Main instructions.\n\nPattern reference content" {
		t.Errorf("prompt = %q", sk.Prompt)
	}
}

func TestParseSkill_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "simple", "Just a prompt body with no frontmatter.")

	sk, err := parseSkill(filepath.Join(dir, "simple"))
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "simple" {
		t.Errorf("name = %q, want simple (dirname)", sk.Name)
	}
	if sk.Description != "" {
		t.Errorf("description = %q, want empty", sk.Description)
	}
	if sk.Prompt != "Just a prompt body with no frontmatter." {
		t.Errorf("prompt = %q", sk.Prompt)
	}
}

func TestFileSkillStore_UserOverridesSystem(t *testing.T) {
	sysDir := t.TempDir()
	userDir := t.TempDir()

	writeSkill(t, sysDir, "coding", `---
description: System coding
---
System prompt.
`)
	writeSkill(t, userDir, "coding", `---
description: User coding
---
User prompt.
`)

	store := NewFileSkillStore(sysDir, userDir)
	sk, ok := store.Get("coding")
	if !ok {
		t.Fatal("coding not found")
	}
	if sk.Description != "User coding" {
		t.Errorf("description = %q, want User coding (user override)", sk.Description)
	}
	if sk.Prompt != "User prompt." {
		t.Errorf("prompt = %q", sk.Prompt)
	}
}

func TestFileSkillStore_Reload(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSkillStore(dir, "")

	all := store.All()
	if len(all) != 0 {
		t.Errorf("expected empty, got %d", len(all))
	}

	// Add a skill and reload.
	writeSkill(t, dir, "new-skill", `---
description: A new skill
---
New prompt.
`)
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}

	all = store.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(all))
	}
	if all[0].Name != "new-skill" {
		t.Errorf("name = %q", all[0].Name)
	}
}

func TestFileSkillStore_MissingDir(t *testing.T) {
	store := NewFileSkillStore("/nonexistent/system", "/nonexistent/user")
	all := store.All()
	if len(all) != 0 {
		t.Errorf("expected empty, got %d", len(all))
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	name, desc, ver, triggers, body := parseFrontmatter(`---
name: "my-skill"
description: 'A quoted description'
version: "2"
triggers: tweet, draft, x
---

Body here.
`)
	if name != "my-skill" {
		t.Errorf("name = %q", name)
	}
	if desc != "A quoted description" {
		t.Errorf("description = %q", desc)
	}
	if ver != "2" {
		t.Errorf("version = %q", ver)
	}
	if len(triggers) != 3 || triggers[0] != "tweet" || triggers[1] != "draft" || triggers[2] != "x" {
		t.Errorf("triggers = %v", triggers)
	}
	if body != "Body here." {
		t.Errorf("body = %q", body)
	}
}
