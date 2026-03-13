package controlcenter

import (
	"testing"
	"time"
)

func TestNewMessageID(t *testing.T) {
	id1 := NewMessageID()
	id2 := NewMessageID()
	if id1 == "" {
		t.Fatal("NewMessageID returned empty string")
	}
	if id1 == id2 {
		t.Errorf("expected unique IDs, got same: %q", id1)
	}
}

func TestChatStore_AppendAndGet(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	msg := ChatMessage{
		ID:        NewMessageID(),
		Role:      "user",
		Text:      "hello",
		Timestamp: time.Now(),
	}
	store.Append(msg)

	got := store.Get(msg.ID)
	if got == nil {
		t.Fatal("Get returned nil for appended message")
	}
	if got.Text != "hello" {
		t.Errorf("expected text %q, got %q", "hello", got.Text)
	}
	if got.Role != "user" {
		t.Errorf("expected role %q, got %q", "user", got.Role)
	}
}

func TestChatStore_GetMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	if got := store.Get("nonexistent"); got != nil {
		t.Errorf("expected nil for missing ID, got %+v", got)
	}
}

func TestChatStore_Recent(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	for i := 0; i < 5; i++ {
		store.Append(ChatMessage{
			ID:        NewMessageID(),
			Role:      "user",
			Text:      string(rune('a' + i)),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	recent := store.Recent(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent, got %d", len(recent))
	}
	// Should be the last 3 in chronological order.
	if recent[0].Text != "c" {
		t.Errorf("expected first recent text %q, got %q", "c", recent[0].Text)
	}
	if recent[2].Text != "e" {
		t.Errorf("expected last recent text %q, got %q", "e", recent[2].Text)
	}
}

func TestChatStore_RecentAll(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	store.Append(ChatMessage{ID: "1", Role: "user", Text: "a", Timestamp: time.Now()})
	store.Append(ChatMessage{ID: "2", Role: "user", Text: "b", Timestamp: time.Now()})

	all := store.Recent(0)
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
}

func TestChatStore_History(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	now := time.Now()
	for i := 0; i < 5; i++ {
		store.Append(ChatMessage{
			ID:        NewMessageID(),
			Role:      "user",
			Text:      string(rune('a' + i)),
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}

	// History before the 3rd message → should return first 2.
	msgs := store.History(10, now.Add(2*time.Minute), "")
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	if msgs[0].Text != "a" || msgs[1].Text != "b" {
		t.Errorf("unexpected texts: %q, %q", msgs[0].Text, msgs[1].Text)
	}

	// History with limit.
	msgs = store.History(1, time.Time{}, "")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 with limit, got %d", len(msgs))
	}
}

func TestChatStore_AddReaction(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	msg := ChatMessage{ID: "msg-1", Role: "assistant", Text: "hi", Timestamp: time.Now()}
	store.Append(msg)

	ok := store.AddReaction("msg-1", Reaction{Emoji: "🔥", From: "user"})
	if !ok {
		t.Fatal("AddReaction returned false for existing message")
	}

	got := store.Get("msg-1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if len(got.Reactions) != 1 {
		t.Fatalf("expected 1 reaction, got %d", len(got.Reactions))
	}
	if got.Reactions[0].Emoji != "🔥" {
		t.Errorf("expected emoji 🔥, got %q", got.Reactions[0].Emoji)
	}

	// Reaction on non-existent message.
	ok = store.AddReaction("nonexistent", Reaction{Emoji: "👍", From: "user"})
	if ok {
		t.Error("AddReaction should return false for missing message")
	}
}

func TestChatStore_RingWrapAround(t *testing.T) {
	dir := t.TempDir()
	store := NewChatStore(dir)

	// Fill beyond ring size.
	for i := 0; i < chatRingSize+10; i++ {
		store.Append(ChatMessage{
			ID:        NewMessageID(),
			Role:      "user",
			Text:      "msg",
			Timestamp: time.Now(),
		})
	}

	all := store.Recent(0)
	if len(all) != chatRingSize {
		t.Errorf("expected ring size %d, got %d", chatRingSize, len(all))
	}
}

func TestChatStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Write messages.
	store1 := NewChatStore(dir)
	store1.Append(ChatMessage{ID: "persist-1", Role: "user", Text: "hello", Timestamp: time.Now()})
	store1.Append(ChatMessage{ID: "persist-2", Role: "assistant", Text: "hi", Timestamp: time.Now()})

	// Load from same directory.
	store2 := NewChatStore(dir)
	got := store2.Get("persist-1")
	if got == nil {
		t.Fatal("persisted message not found after reload")
	}
	if got.Text != "hello" {
		t.Errorf("expected %q, got %q", "hello", got.Text)
	}

	all := store2.Recent(0)
	if len(all) != 2 {
		t.Errorf("expected 2 messages after reload, got %d", len(all))
	}
}
