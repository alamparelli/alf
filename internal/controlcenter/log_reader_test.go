package controlcenter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempLog(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp log: %v", err)
	}
	return dir
}

func TestTailBasic(t *testing.T) {
	dir := writeTempLog(t, "app.log", "line1\nline2\nline3\nline4\nline5\n")
	r := NewFileLogReader(dir, nil)

	lines, err := r.Tail("app.log", 3)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "line3" || lines[2] != "line5" {
		t.Fatalf("unexpected lines: %v", lines)
	}
}

func TestTailDefaultN(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("line\n")
	}
	dir := writeTempLog(t, "big.log", b.String())
	r := NewFileLogReader(dir, nil)

	lines, err := r.Tail("big.log", 0)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(lines) != 200 {
		t.Fatalf("expected default 200 lines, got %d", len(lines))
	}
}

func TestTailMissingFile(t *testing.T) {
	dir := t.TempDir()
	r := NewFileLogReader(dir, nil)

	lines, err := r.Tail("nonexistent.log", 10)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(lines))
	}
}

func TestTailPathTraversal(t *testing.T) {
	dir := t.TempDir()
	r := NewFileLogReader(dir, nil)

	_, err := r.Tail("../etc/passwd", 10)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestTailAllowlist(t *testing.T) {
	dir := writeTempLog(t, "allowed.log", "ok\n")
	writeTempLog(t, "secret.log", "nope\n")
	r := NewFileLogReader(dir, []string{"allowed.log"})

	_, err := r.Tail("allowed.log", 10)
	if err != nil {
		t.Fatalf("allowed file should work: %v", err)
	}

	_, err = r.Tail("secret.log", 10)
	if err == nil {
		t.Fatal("expected error for non-allowlisted file")
	}
}

func TestTailLargeFileNoOOM(t *testing.T) {
	// Create a 3MB log file — should only read last 1MB.
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.log")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	line := strings.Repeat("x", 200) + "\n" // 201 bytes per line
	totalLines := (3 * 1024 * 1024) / len(line)
	for i := 0; i < totalLines; i++ {
		f.WriteString(line)
	}
	f.Close()

	r := NewFileLogReader(dir, nil)
	lines, err := r.Tail("huge.log", 50)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(lines) != 50 {
		t.Fatalf("expected 50 lines, got %d", len(lines))
	}
}

func TestReadTailSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.log")
	os.WriteFile(path, []byte("a\nb\nc\n"), 0644)

	data, err := readTail(path, maxTailBytes)
	if err != nil {
		t.Fatalf("readTail: %v", err)
	}
	if string(data) != "a\nb\nc\n" {
		t.Fatalf("expected full content, got %q", data)
	}
}

func TestReadTailDropsPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	// Write more than 64 bytes, use 64 as maxBytes to test truncation.
	content := "first-long-line-that-will-be-cut\nsecond-line\nthird-line\n"
	os.WriteFile(path, []byte(content), 0644)

	data, err := readTail(path, 30)
	if err != nil {
		t.Fatalf("readTail: %v", err)
	}

	// Should not start with a partial line.
	s := string(data)
	if strings.Contains(s, "first-long") {
		t.Fatalf("partial first line should have been dropped, got: %q", s)
	}
	if !strings.Contains(s, "second-line") {
		t.Fatalf("expected second-line in result, got: %q", s)
	}
}

func TestAvailableAutoDiscover(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.log"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "error.log"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "not-a-log.txt"), []byte("x"), 0644)

	r := NewFileLogReader(dir, nil)
	names := r.Available()

	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["app.log"] || !found["error.log"] {
		t.Fatalf("expected app.log and error.log, got %v", names)
	}
	if found["not-a-log.txt"] {
		t.Fatal("non-.log file should not be discovered")
	}
}
