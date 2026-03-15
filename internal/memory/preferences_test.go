package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendPreference(t *testing.T) {
	dir := t.TempDir()

	AppendPreference(dir, "User likes bullet lists", "positive", "👍")
	AppendPreference(dir, "User dislikes verbose explanations", "negative", "👎")

	data, err := os.ReadFile(filepath.Join(dir, preferencesFile))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "# User Preferences") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "[+] User likes bullet lists (👍)") {
		t.Error("missing positive entry")
	}
	if !strings.Contains(content, "[-] User dislikes verbose explanations (👎)") {
		t.Error("missing negative entry")
	}
}

func TestCountEntries(t *testing.T) {
	dir := t.TempDir()

	if CountEntries(dir) != 0 {
		t.Error("expected 0 for non-existent file")
	}

	for i := 0; i < 5; i++ {
		AppendPreference(dir, "pref", "positive", "👍")
	}

	if got := CountEntries(dir); got != 5 {
		t.Errorf("expected 5 entries, got %d", got)
	}
}

func TestAppendPreference_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, preferencesFile)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should not exist yet")
	}

	AppendPreference(dir, "test", "positive", "👍")

	if _, err := os.Stat(path); err != nil {
		t.Fatal("file should exist after append")
	}
}
