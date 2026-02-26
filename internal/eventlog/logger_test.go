package eventlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLog_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	defer l.Close()

	l.Log("test_event", map[string]any{"key": "value"})

	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "events", today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected log file: %v", err)
	}
	if !strings.Contains(string(data), `"event":"test_event"`) {
		t.Errorf("log line missing event: %s", data)
	}
	if !strings.Contains(string(data), `"key":"value"`) {
		t.Errorf("log line missing field: %s", data)
	}
}

func TestLog_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	defer l.Close()

	l.Log("msg_in", map[string]any{"chat_id": int64(123), "text": "hello"})

	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "events", today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("invalid JSON: %v\nline: %s", err, data)
	}
	if rec["event"] != "msg_in" {
		t.Errorf("unexpected event: %v", rec["event"])
	}
	if rec["ts"] == nil {
		t.Error("missing timestamp")
	}
}

func TestLog_MultipleLines(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	defer l.Close()

	l.Log("a", nil)
	l.Log("b", nil)
	l.Log("c", nil)

	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "events", today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestLog_NilFields(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	defer l.Close()

	l.Log("empty", nil)

	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "events", today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if rec["event"] != "empty" {
		t.Errorf("unexpected event: %v", rec["event"])
	}
}

func TestClose_Idempotent(t *testing.T) {
	l := New(t.TempDir())
	l.Log("x", nil)
	l.Close()
	l.Close() // should not panic
}
