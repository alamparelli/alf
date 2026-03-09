package vault

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

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
