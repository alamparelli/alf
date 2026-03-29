package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
