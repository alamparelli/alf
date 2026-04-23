package controlcenter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseClaudeModels_SkipsBlanksAndComments(t *testing.T) {
	input := []byte(`
# header comment
claude-opus-4-7

  # indented comment
claude-sonnet-4-6
   claude-haiku-4-5
`)
	got := parseClaudeModels(input)
	want := []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5"}
	if !equalSlices(got, want) {
		t.Errorf("parseClaudeModels = %v, want %v", got, want)
	}
}

func TestParseClaudeModels_Dedup(t *testing.T) {
	input := []byte("claude-opus-4-7\nclaude-opus-4-7\nclaude-sonnet-4-6\n")
	got := parseClaudeModels(input)
	want := []string{"claude-opus-4-7", "claude-sonnet-4-6"}
	if !equalSlices(got, want) {
		t.Errorf("dedup failed: got %v, want %v", got, want)
	}
}

func TestParseClaudeModels_Empty(t *testing.T) {
	if got := parseClaudeModels([]byte("")); len(got) != 0 {
		t.Errorf("empty input should yield empty list, got %v", got)
	}
	if got := parseClaudeModels([]byte("# only comments\n# nothing else\n")); len(got) != 0 {
		t.Errorf("comment-only input should yield empty list, got %v", got)
	}
}

func TestDefaultClaudeModelsFile_NonEmpty(t *testing.T) {
	list := parseClaudeModels(defaultClaudeModelsTxt)
	if len(list) == 0 {
		t.Fatal("embedded default claude_models.txt must not parse to empty list")
	}
	// Sanity: embedded list should contain at least one current model.
	found := false
	for _, m := range list {
		if m == "claude-opus-4-7" || m == "claude-sonnet-4-6" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("embedded default should contain a current model, got %v", list)
	}
}

func TestClaudeModelsStore_FallbackOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	s := NewFileClaudeModelsStore(filepath.Join(dir, "does-not-exist.txt"))
	if err := s.Reload(); err != nil {
		t.Fatalf("reload with missing file should not error: %v", err)
	}
	if len(s.Current()) == 0 {
		t.Error("missing file should fall back to embedded default, got empty list")
	}
}

func TestClaudeModelsStore_FallbackOnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_models.txt")
	if err := os.WriteFile(path, []byte("# only comments\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewFileClaudeModelsStore(path)
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(s.Current()) == 0 {
		t.Error("empty parsed file should fall back to embedded default, got empty list")
	}
}

func TestClaudeModelsStore_ReadsUserFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_models.txt")
	if err := os.WriteFile(path, []byte("my-custom-gw/opus-v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewFileClaudeModelsStore(path)
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if !s.Contains("my-custom-gw/opus-v1") {
		t.Errorf("expected custom entry to be loaded, got %v", s.Current())
	}
}

func TestIsValidClaudeModel_ShortAliases(t *testing.T) {
	SetClaudeModelsStore(nil) // no store → hit embedded default
	defer SetClaudeModelsStore(nil)
	for _, alias := range []string{"haiku", "sonnet", "opus", "sonnet-max", "opus-max"} {
		if !IsValidClaudeModel(alias) {
			t.Errorf("short alias %q should be valid (resolver pass-through)", alias)
		}
	}
}

func TestIsValidClaudeModel_ClaudePrefixPassthrough(t *testing.T) {
	SetClaudeModelsStore(nil)
	defer SetClaudeModelsStore(nil)
	// New/future model IDs not yet in the embedded list must still pass via
	// the claude-* resolver pass-through.
	cases := []string{"claude-opus-4-7", "claude-sonnet-5-0", "claude-haiku-5-0-20260101"}
	for _, m := range cases {
		if !IsValidClaudeModel(m) {
			t.Errorf("claude-* prefix %q should be valid", m)
		}
	}
}

func TestIsValidClaudeModel_CustomEntryFromStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_models.txt")
	if err := os.WriteFile(path, []byte("my-gw/custom-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewFileClaudeModelsStore(path)
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	SetClaudeModelsStore(s)
	defer SetClaudeModelsStore(nil)

	if !IsValidClaudeModel("my-gw/custom-model") {
		t.Error("custom entry from store should be valid")
	}
	if IsValidClaudeModel("unknown-garbage") {
		t.Error("garbage model should be rejected even with a store set")
	}
}

func TestIsValidClaudeModel_RejectsEmptyAndGarbage(t *testing.T) {
	SetClaudeModelsStore(nil)
	defer SetClaudeModelsStore(nil)
	if IsValidClaudeModel("") {
		t.Error("empty model should be rejected")
	}
	if IsValidClaudeModel("gpt-4o") {
		t.Error("non-claude model should be rejected for cli backend")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
