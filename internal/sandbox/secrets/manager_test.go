package secrets

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression guard for #385-1: the daemon vault socket must be 0660
// (group-rw for alfd, world-no-access). Ticket #385 flagged the prior
// 0666 as an attack-surface smell even though vault-data/ (0700) already
// gates the path — if a future change ever moves the socket, the mode
// must not silently widen to world-rw.
func TestVaultSocketMode_Is0660(t *testing.T) {
	if got := vaultSocketMode & os.ModePerm; got != 0660 {
		t.Fatalf("vaultSocketMode = %o, want 0660", got)
	}

	if runtime.GOOS == "windows" {
		t.Skip("POSIX perm bits not meaningful on Windows")
	}

	// End-to-end sanity: chmod a real file and read back the mode.
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.sock")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()

	if err := os.Chmod(path, vaultSocketMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0660 {
		t.Fatalf("on-disk mode = %o, want 0660", got)
	}
}

func captureLog(fn func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0) // no timestamps for test comparison
	defer func() {
		log.SetOutput(nil)
		log.SetFlags(log.LstdFlags)
	}()
	fn()
	return buf.String()
}

func TestLogWriter_MultipleLines(t *testing.T) {
	w := &logWriter{prefix: "[test] "}
	output := captureLog(func() {
		w.Write([]byte("line one\nline two\nline three\n"))
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 log lines, got %d: %v", len(lines), lines)
	}

	expected := []string{"[test] line one", "[test] line two", "[test] line three"}
	for i, want := range expected {
		if lines[i] != want {
			t.Errorf("line %d: expected %q, got %q", i, want, lines[i])
		}
	}
}

func TestLogWriter_PartialLine_Buffered(t *testing.T) {
	w := &logWriter{prefix: "[buf] "}

	// Write partial line (no newline) -- should buffer, not log.
	output := captureLog(func() {
		w.Write([]byte("partial"))
	})
	if output != "" {
		t.Errorf("expected no output for partial line, got %q", output)
	}

	// Internal buffer should hold the partial data.
	if string(w.buf) != "partial" {
		t.Errorf("expected buffer to contain 'partial', got %q", string(w.buf))
	}

	// Completing the line should flush.
	output = captureLog(func() {
		w.Write([]byte(" end\n"))
	})
	if !strings.Contains(output, "[buf] partial end") {
		t.Errorf("expected completed line in output, got %q", output)
	}

	// Buffer should be empty after flush.
	if len(w.buf) != 0 {
		t.Errorf("expected empty buffer after newline, got %q", string(w.buf))
	}
}

func TestLogWriter_EmptyLines_Skipped(t *testing.T) {
	w := &logWriter{prefix: "[skip] "}

	// Empty lines (just newlines) should be skipped per the implementation.
	output := captureLog(func() {
		w.Write([]byte("\n\n\n"))
	})
	if output != "" {
		t.Errorf("expected no output for empty lines, got %q", output)
	}
}

func TestLogWriter_MixedEmptyAndContent(t *testing.T) {
	w := &logWriter{prefix: "[mix] "}

	output := captureLog(func() {
		w.Write([]byte("\nfirst\n\nsecond\n"))
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines (empty skipped), got %d: %v", len(lines), lines)
	}
	if lines[0] != "[mix] first" {
		t.Errorf("expected '[mix] first', got %q", lines[0])
	}
	if lines[1] != "[mix] second" {
		t.Errorf("expected '[mix] second', got %q", lines[1])
	}
}

func TestLogWriter_ReturnValue(t *testing.T) {
	w := &logWriter{prefix: ""}

	data := []byte("hello\nworld\n")
	captureLog(func() {
		n, err := w.Write(data)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if n != len(data) {
			t.Errorf("expected Write to return %d, got %d", len(data), n)
		}
	})
}

func TestLogWriter_MultipleSmallWrites(t *testing.T) {
	w := &logWriter{prefix: "[chunk] "}

	// Simulate chunked output from a subprocess.
	output := captureLog(func() {
		w.Write([]byte("hel"))
		w.Write([]byte("lo "))
		w.Write([]byte("world\nbye\n"))
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "[chunk] hello world" {
		t.Errorf("expected '[chunk] hello world', got %q", lines[0])
	}
	if lines[1] != "[chunk] bye" {
		t.Errorf("expected '[chunk] bye', got %q", lines[1])
	}
}
