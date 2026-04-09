package chatdb

import (
	"fmt"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("chatdb.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInsertMessage_SeqAutoIncrement(t *testing.T) {
	db := newTestDB(t)
	convID := "conv-1"
	db.EnsureConversation(convID, "test", "cc")

	for i := 0; i < 5; i++ {
		err := db.InsertMessage(Message{
			ID:     fmt.Sprintf("msg-%d", i),
			ConvID: convID,
			Role:   "user",
			Text:   "msg",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	msgs, err := db.History(convID, 50, time.Time{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		expected := int64(i + 1)
		if m.Seq != expected {
			t.Errorf("msg[%d].Seq = %d, want %d", i, m.Seq, expected)
		}
	}
}

func TestInsertMessage_SeqPerConversation(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "conv A", "cc")
	db.EnsureConversation("b", "conv B", "cc")

	db.InsertMessage(Message{ID: "a1", ConvID: "a", Role: "user", Text: "1"})
	db.InsertMessage(Message{ID: "b1", ConvID: "b", Role: "user", Text: "1"})
	db.InsertMessage(Message{ID: "a2", ConvID: "a", Role: "user", Text: "2"})
	db.InsertMessage(Message{ID: "b2", ConvID: "b", Role: "user", Text: "2"})
	db.InsertMessage(Message{ID: "a3", ConvID: "a", Role: "user", Text: "3"})

	msgsA, _ := db.History("a", 50, time.Time{})
	msgsB, _ := db.History("b", 50, time.Time{})

	if len(msgsA) != 3 {
		t.Fatalf("conv A: expected 3, got %d", len(msgsA))
	}
	if len(msgsB) != 2 {
		t.Fatalf("conv B: expected 2, got %d", len(msgsB))
	}

	for i, m := range msgsA {
		if m.Seq != int64(i+1) {
			t.Errorf("conv A msg[%d].Seq = %d, want %d", i, m.Seq, i+1)
		}
	}
	for i, m := range msgsB {
		if m.Seq != int64(i+1) {
			t.Errorf("conv B msg[%d].Seq = %d, want %d", i, m.Seq, i+1)
		}
	}
}

func TestSetGetMeta(t *testing.T) {
	db := newTestDB(t)

	// Get missing key returns empty string.
	if v := db.GetMeta("nonexistent"); v != "" {
		t.Errorf("expected empty for missing key, got %q", v)
	}

	// Set and get.
	if err := db.SetMeta("active_conv_id", "conv-123"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if v := db.GetMeta("active_conv_id"); v != "conv-123" {
		t.Errorf("expected conv-123, got %q", v)
	}

	// Overwrite existing key.
	if err := db.SetMeta("active_conv_id", "conv-456"); err != nil {
		t.Fatalf("SetMeta overwrite: %v", err)
	}
	if v := db.GetMeta("active_conv_id"); v != "conv-456" {
		t.Errorf("expected conv-456 after overwrite, got %q", v)
	}

	// Multiple keys don't interfere.
	db.SetMeta("other_key", "other_value")
	if v := db.GetMeta("active_conv_id"); v != "conv-456" {
		t.Errorf("expected conv-456, got %q", v)
	}
	if v := db.GetMeta("other_key"); v != "other_value" {
		t.Errorf("expected other_value, got %q", v)
	}
}

func TestHistory_OrderBySeq_SameTimestamp(t *testing.T) {
	db := newTestDB(t)
	convID := "conv-order"
	db.EnsureConversation(convID, "", "cc")

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		db.InsertMessage(Message{
			ID:        fmt.Sprintf("msg-%d", i),
			ConvID:    convID,
			Role:      "user",
			Text:      "msg",
			CreatedAt: ts,
		})
	}

	msgs, _ := db.History(convID, 50, time.Time{})
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Seq <= msgs[i-1].Seq {
			t.Errorf("msgs[%d].Seq (%d) <= msgs[%d].Seq (%d)", i, msgs[i].Seq, i-1, msgs[i-1].Seq)
		}
	}
}
