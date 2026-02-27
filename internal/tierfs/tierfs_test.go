package tierfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDir(t *testing.T) {
	dir := t.TempDir()
	tfs := New(dir)

	if err := tfs.EnsureDir("analyze"); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Check directory structure.
	if _, err := os.Stat(filepath.Join(dir, "tiers", "analyze", "skills")); err != nil {
		t.Errorf("expected skills dir to exist: %v", err)
	}
}

func TestSystemPrompt_ReadWrite(t *testing.T) {
	dir := t.TempDir()
	tfs := New(dir)
	tfs.EnsureDir("heavy")

	// Empty by default.
	if sp := tfs.SystemPrompt("heavy"); sp != "" {
		t.Errorf("expected empty system prompt, got %q", sp)
	}

	// Write and read back.
	if err := tfs.WriteSystemPrompt("heavy", "You are a code reviewer."); err != nil {
		t.Fatalf("WriteSystemPrompt: %v", err)
	}
	if sp := tfs.SystemPrompt("heavy"); sp != "You are a code reviewer." {
		t.Errorf("expected written content, got %q", sp)
	}
}

func TestCollectPromptArgs_Empty(t *testing.T) {
	dir := t.TempDir()
	tfs := New(dir)
	tfs.EnsureDir("instant")

	args := tfs.CollectPromptArgs("instant")
	if len(args) != 0 {
		t.Errorf("expected no args for empty tier, got %v", args)
	}
}

func TestCollectPromptArgs_WithContent(t *testing.T) {
	dir := t.TempDir()
	tfs := New(dir)
	tfs.EnsureDir("heavy")

	tfs.WriteSystemPrompt("heavy", "Be thorough.")

	// Add a skill.
	store := tfs.SkillStore("heavy")
	store.Put("review", []byte("Review all code changes carefully."))

	args := tfs.CollectPromptArgs("heavy")

	// Should have 4 elements: 2 pairs (system prompt + skill).
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}

	if args[0] != "--append-system-prompt" {
		t.Errorf("expected --append-system-prompt, got %q", args[0])
	}
	if !strings.Contains(args[1], "Be thorough.") {
		t.Errorf("expected system prompt content in args[1], got %q", args[1])
	}
	if !strings.Contains(args[3], "Review all code changes carefully.") {
		t.Errorf("expected skill content in args[3], got %q", args[3])
	}
}

func TestRenameDir(t *testing.T) {
	dir := t.TempDir()
	tfs := New(dir)
	tfs.EnsureDir("old-tier")
	tfs.WriteSystemPrompt("old-tier", "content")

	if err := tfs.RenameDir("old-tier", "new-tier"); err != nil {
		t.Fatalf("RenameDir: %v", err)
	}

	if sp := tfs.SystemPrompt("new-tier"); sp != "content" {
		t.Errorf("expected content after rename, got %q", sp)
	}
	if sp := tfs.SystemPrompt("old-tier"); sp != "" {
		t.Errorf("old dir should not exist after rename")
	}
}

func TestRenameDir_NonExistent(t *testing.T) {
	dir := t.TempDir()
	tfs := New(dir)

	// Should not error on non-existent dir.
	if err := tfs.RenameDir("nope", "new"); err != nil {
		t.Errorf("expected no error for non-existent dir, got %v", err)
	}
}
