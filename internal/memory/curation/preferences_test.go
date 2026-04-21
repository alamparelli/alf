package curation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/curation"
)

// stubInvoker captures the prompt seen by curation.ConsolidatePreferences and
// returns a canned reply (or error). Used only by preference-consolidation tests.
type stubInvoker struct {
	reply string
	err   error
	seen  string
}

func (s *stubInvoker) Invoke(_ context.Context, prompt string) (string, error) {
	s.seen = prompt
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

func (s *stubInvoker) Func() curation.PrefInvoker { return s.Invoke }

func TestConsolidatePreferences_BelowThresholdNoOp(t *testing.T) {
	dir := t.TempDir()

	// 3 entries < memory.PreferencesThreshold (20).
	for i := 0; i < 3; i++ {
		memory.AppendPreference(dir, "p", "positive", "👍")
	}

	original, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))
	stub := &stubInvoker{reply: "should-never-run"}

	curation.ConsolidatePreferences(dir, stub.Func())

	if stub.seen != "" {
		t.Error("invoker called despite being below threshold")
	}

	after, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))
	if string(after) != string(original) {
		t.Errorf("file mutated despite no-op:\nbefore=%q\nafter=%q", string(original), string(after))
	}
}

func TestConsolidatePreferences_AboveThresholdReplacesFile(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < memory.PreferencesThreshold+1; i++ {
		memory.AppendPreference(dir, "pref", "positive", "👍")
	}

	consolidated := "# User Preferences\n\n## Communication\n- [+] concise answers\n"
	stub := &stubInvoker{reply: consolidated}

	curation.ConsolidatePreferences(dir, stub.Func())

	if stub.seen == "" {
		t.Fatal("invoker was not called despite being above threshold")
	}

	got, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))
	if !strings.Contains(string(got), "## Communication") {
		t.Errorf("file not replaced with consolidated content:\n%s", string(got))
	}
}

func TestConsolidatePreferences_StripsMarkdownFences(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < memory.PreferencesThreshold+1; i++ {
		memory.AppendPreference(dir, "pref", "positive", "👍")
	}

	fenced := "```markdown\n# User Preferences\n\n## Tone\n- [+] direct\n```"
	stub := &stubInvoker{reply: fenced}

	curation.ConsolidatePreferences(dir, stub.Func())

	got, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))
	if strings.Contains(string(got), "```") {
		t.Errorf("markdown fences leaked into file:\n%s", string(got))
	}
	if !strings.Contains(string(got), "## Tone") {
		t.Errorf("content dropped:\n%s", string(got))
	}
}

func TestConsolidatePreferences_RejectsUnexpectedFormat(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < memory.PreferencesThreshold+1; i++ {
		memory.AppendPreference(dir, "pref", "positive", "👍")
	}
	original, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))

	stub := &stubInvoker{reply: "lorem ipsum without the expected header"}
	curation.ConsolidatePreferences(dir, stub.Func())

	after, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))
	if string(after) != string(original) {
		t.Errorf("file replaced with invalid format:\n%s", string(after))
	}
}

func TestConsolidatePreferences_InvokerErrorLeavesFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < memory.PreferencesThreshold+1; i++ {
		memory.AppendPreference(dir, "pref", "positive", "👍")
	}
	original, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))

	stub := &stubInvoker{err: errors.New("llm unreachable")}
	curation.ConsolidatePreferences(dir, stub.Func())

	after, _ := os.ReadFile(filepath.Join(dir, memory.PreferencesFile))
	if string(after) != string(original) {
		t.Errorf("invoker error should preserve file, got:\n%s", string(after))
	}
}
