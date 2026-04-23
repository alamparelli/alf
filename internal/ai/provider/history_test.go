package provider

import (
	"os"
	"testing"
	"time"
)

func TestHistory_AppendAndGet(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 100, time.Hour)

	h.Append("test-1", Message{Role: "user", Content: "hello"})
	h.Append("test-1", Message{Role: "assistant", Content: "hi"})

	msgs := h.Get("test-1")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", msgs[0].Content)
	}
	if msgs[1].Content != "hi" {
		t.Errorf("expected 'hi', got %q", msgs[1].Content)
	}
}

func TestHistory_SlidingWindow(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 4, time.Hour) // max 4 messages

	h.Append("w", Message{Role: "user", Content: "m1"})
	h.Append("w", Message{Role: "assistant", Content: "m2"})
	h.Append("w", Message{Role: "user", Content: "m3"})
	h.Append("w", Message{Role: "assistant", Content: "m4"})
	h.Append("w", Message{Role: "user", Content: "m5"})
	h.Append("w", Message{Role: "assistant", Content: "m6"})

	msgs := h.Get("w")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages after sliding window, got %d", len(msgs))
	}
	if msgs[0].Content != "m3" {
		t.Errorf("expected oldest to be m3, got %q", msgs[0].Content)
	}
}

func TestHistory_Clear(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 100, time.Hour)

	h.Append("c", Message{Role: "user", Content: "hello"})
	h.Clear("c")

	msgs := h.Get("c")
	if msgs != nil {
		t.Fatalf("expected nil after clear, got %v", msgs)
	}
}

func TestHistory_Expiry(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 100, 1*time.Millisecond) // very short expiry

	h.Append("e", Message{Role: "user", Content: "hello"})
	time.Sleep(5 * time.Millisecond)

	msgs := h.Get("e")
	if msgs != nil {
		t.Fatalf("expected nil after expiry, got %v", msgs)
	}
}

func TestHistory_Persistence(t *testing.T) {
	dir := t.TempDir()
	h1 := NewHistory(dir, 100, time.Hour)
	h1.Append("p", Message{Role: "user", Content: "persisted"})

	// Create a new History instance pointing to the same dir.
	h2 := NewHistory(dir, 100, time.Hour)
	msgs := h2.Get("p")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message from disk, got %d", len(msgs))
	}
	if msgs[0].Content != "persisted" {
		t.Errorf("expected 'persisted', got %q", msgs[0].Content)
	}
}

func TestHistory_IsolatedSessions(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 100, time.Hour)

	h.Append("a", Message{Role: "user", Content: "for-a"})
	h.Append("b", Message{Role: "user", Content: "for-b"})

	a := h.Get("a")
	b := h.Get("b")
	if len(a) != 1 || a[0].Content != "for-a" {
		t.Errorf("session a: unexpected %v", a)
	}
	if len(b) != 1 || b[0].Content != "for-b" {
		t.Errorf("session b: unexpected %v", b)
	}
}

func TestHistory_EmptyGet(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 100, time.Hour)
	msgs := h.Get("nonexistent")
	if msgs != nil {
		t.Fatalf("expected nil for nonexistent key, got %v", msgs)
	}
}

func TestSanitizeKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tg:12345", "tg_12345"},
		{"simple", "simple"},
		{"a/b/c", "a_b_c"},
		{"", "default"},
	}
	for _, c := range cases {
		got := sanitizeKey(c.in)
		if got != c.want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHistory_PersistenceFile(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir, 100, time.Hour)
	h.Append("disk", Message{Role: "user", Content: "test"})

	// Verify the file exists on disk.
	path := h.filePath("disk")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected history file to exist on disk")
	}

	h.Clear("disk")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected history file to be removed after clear")
	}
}
