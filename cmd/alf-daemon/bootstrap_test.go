package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncClaudeJSON_FreshInstall verifies that a fresh install (no volume
// copy, no backups) produces a valid .claude.json stub so Claude CLI
// subprocesses (classifier, provider) don't warn on every call. See #229.
func TestSyncClaudeJSON_FreshInstall(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	syncClaudeJSON(home)

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf(".claude.json not created on fresh install: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("stub is not valid JSON: %v", err)
	}
	if cfg["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding=%v, want true", cfg["hasCompletedOnboarding"])
	}
}

func TestSyncClaudeJSON_RestoresFromVolume(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(claudeDir, "claude.json"),
		[]byte(`{"token":"from-volume","hasCompletedOnboarding":true}`), 0o640)

	syncClaudeJSON(home)

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if !strings.Contains(string(data), "from-volume") {
		t.Errorf("expected restore from volume, got: %s", data)
	}
}

func TestSyncClaudeJSON_RestoresFromBackup(t *testing.T) {
	home := t.TempDir()
	backupDir := filepath.Join(home, ".claude", "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupDir, ".claude.json.backup.1234567890")
	os.WriteFile(backup, []byte(`{"token":"from-backup"}`), 0o640)

	syncClaudeJSON(home)

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if !strings.Contains(string(data), "from-backup") {
		t.Errorf("expected restore from backup, got: %s", data)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("expected backup file to be removed after restore")
	}
}

func TestSyncClaudeJSON_PreservesExisting(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(home, ".claude.json")
	os.WriteFile(realFile,
		[]byte(`{"token":"keep-me","hasCompletedOnboarding":true,"numStartups":5}`), 0o640)

	syncClaudeJSON(home)

	data, _ := os.ReadFile(realFile)
	if !strings.Contains(string(data), "keep-me") {
		t.Errorf("existing token lost: %s", data)
	}
}

func TestReadSkillVersion_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("---\nname: test\nversion: 1.2.3\n---\nContent here.\n"), 0o644)

	got := readSkillVersion(path)
	if got != "1.2.3" {
		t.Errorf("readSkillVersion = %q, want 1.2.3", got)
	}
}

func TestReadSkillVersion_QuotedValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("---\nversion: \"2.0.0\"\n---\n"), 0o644)

	got := readSkillVersion(path)
	if got != "2.0.0" {
		t.Errorf("readSkillVersion = %q, want 2.0.0", got)
	}
}

func TestReadSkillVersion_MissingFile(t *testing.T) {
	got := readSkillVersion("/nonexistent/SKILL.md")
	if got != "" {
		t.Errorf("readSkillVersion missing file = %q, want empty", got)
	}
}

func TestReadSkillVersion_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("# Just a heading\nNo frontmatter here.\n"), 0o644)

	got := readSkillVersion(path)
	if got != "" {
		t.Errorf("readSkillVersion no frontmatter = %q, want empty", got)
	}
}

func TestReadSkillVersion_FrontmatterNoVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("---\nname: test\ntrigger: /test\n---\nContent.\n"), 0o644)

	got := readSkillVersion(path)
	if got != "" {
		t.Errorf("readSkillVersion no version field = %q, want empty", got)
	}
}

func TestReadSkillVersion_UnclosedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("---\nversion: 1.0.0\nno closing delimiter"), 0o644)

	got := readSkillVersion(path)
	if got != "" {
		t.Errorf("readSkillVersion unclosed frontmatter = %q, want empty", got)
	}
}

func TestBundledSkillNewer_SrcNewer(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nversion: 2.0.0\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)

	if !bundledSkillNewer(src, dst) {
		t.Error("expected bundledSkillNewer=true when src=2.0.0 > dst=1.0.0")
	}
}

func TestBundledSkillNewer_SameVersion(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)

	if bundledSkillNewer(src, dst) {
		t.Error("expected bundledSkillNewer=false when versions are equal")
	}
}

func TestBundledSkillNewer_DstNewer(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("---\nversion: 2.0.0\n---\n"), 0o644)

	if bundledSkillNewer(src, dst) {
		t.Error("expected bundledSkillNewer=false when dst is newer")
	}
}

func TestBundledSkillNewer_MissingSrcSkill(t *testing.T) {
	src := t.TempDir() // no SKILL.md
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)

	if bundledSkillNewer(src, dst) {
		t.Error("expected bundledSkillNewer=false when src has no SKILL.md")
	}
}

func TestBundledSkillNewer_MissingDstSkill(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir() // no SKILL.md
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)

	if bundledSkillNewer(src, dst) {
		t.Error("expected bundledSkillNewer=false when dst has no SKILL.md")
	}
}

func TestBundledSkillNewer_NoFrontmatter(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# No frontmatter\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("---\nversion: 1.0.0\n---\n"), 0o644)

	if bundledSkillNewer(src, dst) {
		t.Error("expected bundledSkillNewer=false when src has no frontmatter")
	}
}
