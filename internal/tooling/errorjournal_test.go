package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorJournal_AppendAndUnresolved(t *testing.T) {
	dir := t.TempDir()
	j := NewErrorJournal(dir)

	j.Append("my-tool", `{"action":"list"}`, "command not found: jq")
	j.Append("my-tool", `{"action":"get"}`, "timeout after 30s")
	j.Append("other-tool", `{}`, "permission denied")

	unresolved := j.Unresolved()
	if len(unresolved) != 3 {
		t.Fatalf("expected 3 unresolved, got %d", len(unresolved))
	}

	if unresolved[0].Tool != "my-tool" {
		t.Errorf("expected tool my-tool, got %s", unresolved[0].Tool)
	}
	if unresolved[0].Error != "command not found: jq" {
		t.Errorf("unexpected error: %s", unresolved[0].Error)
	}
}

func TestErrorJournal_ResolveByName(t *testing.T) {
	dir := t.TempDir()
	j := NewErrorJournal(dir)

	j.Append("my-tool", `{}`, "error 1")
	j.Append("my-tool", `{}`, "error 2")
	j.Append("other-tool", `{}`, "error 3")

	j.ResolveByName("my-tool")

	unresolved := j.Unresolved()
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved after resolve, got %d", len(unresolved))
	}
	if unresolved[0].Tool != "other-tool" {
		t.Errorf("expected other-tool, got %s", unresolved[0].Tool)
	}
}

func TestErrorJournal_RingBuffer(t *testing.T) {
	dir := t.TempDir()
	j := NewErrorJournal(dir)

	for i := 0; i < maxJournalEntries+50; i++ {
		j.Append("tool", `{}`, "error")
	}

	// Read raw file to count lines.
	data, err := os.ReadFile(filepath.Join(dir, "logs", "error-journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != maxJournalEntries {
		t.Errorf("expected %d lines, got %d", maxJournalEntries, lines)
	}
}

func TestErrorJournal_UnresolvedSummary(t *testing.T) {
	dir := t.TempDir()
	j := NewErrorJournal(dir)

	j.Append("broken-tool", `{"action":"run"}`, "ImportError: no module named requests")
	j.Append("broken-tool", `{"action":"run"}`, "ImportError: no module named requests")
	j.Append("flaky-tool", `{}`, "connection refused")

	summary := j.UnresolvedSummary()

	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	// Check it mentions both tools.
	for _, want := range []string{"broken-tool", "flaky-tool", "2 errors", "1 errors", "ImportError"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestErrorJournal_EmptySummary(t *testing.T) {
	dir := t.TempDir()
	j := NewErrorJournal(dir)

	if s := j.UnresolvedSummary(); s != "" {
		t.Errorf("expected empty summary, got: %s", s)
	}
}

func TestErrorJournal_SourceHash(t *testing.T) {
	dir := t.TempDir()

	// Create a tool file so hash can be computed.
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(toolsDir, 0o755)
	os.WriteFile(filepath.Join(toolsDir, "my-tool"), []byte("#!/bin/bash\necho hello"), 0o755)

	j := NewErrorJournal(dir)
	j.Append("my-tool", `{}`, "some error")

	unresolved := j.Unresolved()
	if len(unresolved) != 1 {
		t.Fatal("expected 1 entry")
	}
	if unresolved[0].SourceHash == "" {
		t.Error("expected non-empty source hash")
	}
}

func TestErrorJournal_Persistence(t *testing.T) {
	dir := t.TempDir()

	j1 := NewErrorJournal(dir)
	j1.Append("tool-a", `{}`, "error a")

	// Create new journal instance — should read from same file.
	j2 := NewErrorJournal(dir)
	unresolved := j2.Unresolved()
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved from new instance, got %d", len(unresolved))
	}
}

