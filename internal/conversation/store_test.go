package conversation

import (
	"os"
	"testing"
	"time"
)

func TestStoreAppendAndRecent(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	ch := "test"
	cid := s.ConvID(ch)

	msg1 := Message{
		ID:        "m1",
		ConvID:    cid,
		Channel:   ch,
		Role:      "user",
		Blocks:    []ContentBlock{{Type: BlockText, Text: "hello"}},
		Timestamp: time.Now(),
	}
	msg2 := Message{
		ID:        "m2",
		ConvID:    cid,
		Channel:   ch,
		Role:      "assistant",
		Blocks:    []ContentBlock{{Type: BlockText, Text: "hi there"}},
		Timestamp: time.Now(),
	}

	s.Append(msg1)
	s.Append(msg2)

	recent := s.Recent(ch, 10)
	if len(recent) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(recent))
	}
	if recent[0].ID != "m1" || recent[1].ID != "m2" {
		t.Errorf("messages out of order: %s, %s", recent[0].ID, recent[1].ID)
	}
}

func TestStoreGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	msg := Message{
		ID:      "find-me",
		ConvID:  s.ConvID("ch"),
		Channel: "ch",
		Role:    "user",
		Blocks:  []ContentBlock{{Type: BlockText, Text: "test"}},
	}
	s.Append(msg)

	found := s.Get("find-me")
	if found == nil {
		t.Fatal("expected to find message")
	}
	if found.TextContent() != "test" {
		t.Errorf("expected text 'test', got %q", found.TextContent())
	}

	if s.Get("not-here") != nil {
		t.Error("expected nil for missing message")
	}
}

func TestStoreRecentLimit(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ch := "ch"
	cid := s.ConvID(ch)

	for i := 0; i < 5; i++ {
		s.Append(Message{
			ID:      NewMessageID(),
			ConvID:  cid,
			Channel: ch,
			Role:    "user",
			Blocks:  []ContentBlock{{Type: BlockText, Text: "msg"}},
		})
	}

	recent := s.Recent(ch, 3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(recent))
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	ch := "ch"

	// Write some messages.
	s1 := NewStore(dir)
	cid := s1.ConvID(ch)
	s1.Append(Message{
		ID:      "persist-1",
		ConvID:  cid,
		Channel: ch,
		Role:    "user",
		Blocks:  []ContentBlock{{Type: BlockText, Text: "persisted"}},
	})
	s1.Append(Message{
		ID:      "persist-2",
		ConvID:  cid,
		Channel: ch,
		Role:    "assistant",
		Blocks: []ContentBlock{
			{Type: BlockThinking, Text: "let me think..."},
			{Type: BlockText, Text: "response"},
		},
	})

	// Load from disk.
	s2 := NewStore(dir)
	recent := s2.Recent(ch, 10)
	if len(recent) != 2 {
		t.Fatalf("expected 2 messages after reload, got %d", len(recent))
	}
	if recent[0].ID != "persist-1" {
		t.Errorf("expected first message ID 'persist-1', got %q", recent[0].ID)
	}
	if len(recent[1].Blocks) != 2 {
		t.Errorf("expected 2 blocks in second message, got %d", len(recent[1].Blocks))
	}
}

func TestStoreRingOverflow(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ch := "ch"
	cid := s.ConvID(ch)

	// Fill past ring size.
	for i := 0; i < ringSize+10; i++ {
		s.Append(Message{
			ID:      NewMessageID(),
			ConvID:  cid,
			Channel: ch,
			Role:    "user",
			Blocks:  []ContentBlock{{Type: BlockText, Text: "msg"}},
		})
	}

	recent := s.Recent(ch, 0)
	if len(recent) != ringSize {
		t.Fatalf("expected %d messages (ring size), got %d", ringSize, len(recent))
	}
}

func TestStoreAddReaction(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	s.Append(Message{
		ID:      "react-me",
		ConvID:  s.ConvID("ch"),
		Channel: "ch",
		Role:    "assistant",
		Blocks:  []ContentBlock{{Type: BlockText, Text: "nice"}},
	})

	ok := s.AddReaction("react-me", Reaction{Emoji: "thumbsup", From: "user"})
	if !ok {
		t.Fatal("expected AddReaction to succeed")
	}

	msg := s.Get("react-me")
	if len(msg.Reactions) != 1 {
		t.Fatalf("expected 1 reaction, got %d", len(msg.Reactions))
	}

	ok = s.AddReaction("no-such-msg", Reaction{Emoji: "x", From: "user"})
	if ok {
		t.Error("expected AddReaction to fail for missing message")
	}
}

func TestStoreConvIDScoping(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ch := "tg"
	cid := s.ConvID(ch)

	// Add messages to first conversation.
	s.Append(Message{ID: "conv1-m1", ConvID: cid, Channel: ch, Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hello"}}})
	s.Append(Message{ID: "conv1-m2", ConvID: cid, Channel: ch, Role: "assistant", Blocks: []ContentBlock{{Type: BlockText, Text: "hi"}}})

	if len(s.Recent(ch, 0)) != 2 {
		t.Fatalf("expected 2 messages in conv1, got %d", len(s.Recent(ch, 0)))
	}

	// Start new conversation.
	oldConvID := s.NewConversation(ch)
	if oldConvID == "" {
		t.Fatal("expected non-empty old conv ID")
	}

	// Recent should be empty now.
	if len(s.Recent(ch, 0)) != 0 {
		t.Fatalf("expected 0 messages after NewConversation, got %d", len(s.Recent(ch, 0)))
	}

	// Add to new conversation.
	newCID := s.ConvID(ch)
	s.Append(Message{ID: "conv2-m1", ConvID: newCID, Channel: ch, Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "new topic"}}})

	recent := s.Recent(ch, 0)
	if len(recent) != 1 {
		t.Fatalf("expected 1 message in conv2, got %d", len(recent))
	}
	if recent[0].ID != "conv2-m1" {
		t.Errorf("expected conv2-m1, got %s", recent[0].ID)
	}

	// RecentAll should return all messages across conversations.
	all := s.RecentAll(0)
	if len(all) != 3 {
		t.Fatalf("expected 3 total messages, got %d", len(all))
	}
}

func TestStoreChannelIsolation(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	tgCID := s.ConvID("tg")
	ccCID := s.ConvID("cc")

	s.Append(Message{ID: "tg-1", ConvID: tgCID, Channel: "tg", Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "tg msg"}}})
	s.Append(Message{ID: "cc-1", ConvID: ccCID, Channel: "cc", Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "cc msg"}}})

	tgRecent := s.Recent("tg", 0)
	ccRecent := s.Recent("cc", 0)

	if len(tgRecent) != 1 || tgRecent[0].ID != "tg-1" {
		t.Errorf("tg channel: expected [tg-1], got %v", tgRecent)
	}
	if len(ccRecent) != 1 || ccRecent[0].ID != "cc-1" {
		t.Errorf("cc channel: expected [cc-1], got %v", ccRecent)
	}

	// NewConversation on tg doesn't affect cc.
	s.NewConversation("tg")
	if len(s.Recent("tg", 0)) != 0 {
		t.Error("tg should be empty after NewConversation")
	}
	if len(s.Recent("cc", 0)) != 1 {
		t.Error("cc should still have 1 message")
	}
}

func TestStoreConvIDResumesFromDisk(t *testing.T) {
	dir := t.TempDir()
	ch := "tg"

	// Write messages with a known conv ID.
	s1 := NewStore(dir)
	cid := s1.ConvID(ch)
	s1.Append(Message{ID: "p1", ConvID: cid, Channel: ch, Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hi"}}})

	// Reload from disk - should resume the same conv ID.
	s2 := NewStore(dir)
	if s2.ConvID(ch) != cid {
		t.Errorf("expected convID %q after reload, got %q", cid, s2.ConvID(ch))
	}

	// Messages from the resumed conversation should be visible.
	recent := s2.Recent(ch, 0)
	if len(recent) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(recent))
	}
}

func TestStoreFileCreated(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.Append(Message{ID: "test", ConvID: s.ConvID("ch"), Channel: "ch", Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hi"}}})

	_, err := os.Stat(s.filePath)
	if err != nil {
		t.Fatalf("expected JSONL file to exist: %v", err)
	}
}

// --- BuildRouterContext tests ---

func TestBuildRouterContext_Empty(t *testing.T) {
	if result := BuildRouterContext(nil, 3); result != "" {
		t.Errorf("expected empty for nil, got %q", result)
	}
	if result := BuildRouterContext([]Message{}, 3); result != "" {
		t.Errorf("expected empty for empty slice, got %q", result)
	}
}

func TestBuildRouterContext_BasicExchange(t *testing.T) {
	msgs := []Message{
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "a regarder: https://youtube.com/watch?v=abc"}}},
		{Role: "assistant", Tier: "sonnet", Blocks: []ContentBlock{{Type: BlockText, Text: "Ajouté, mais je n'ai pas pu récupérer le titre."}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "1089 pixels pour comprendre que vous n'existez pas."}}},
	}
	result := BuildRouterContext(msgs, 3)
	if result == "" {
		t.Fatal("expected non-empty context")
	}
	for _, want := range []string{"[sonnet]", "[user]", "youtube.com"} {
		if !strContains(result, want) {
			t.Errorf("expected %q in context, got:\n%s", want, result)
		}
	}
}

func TestBuildRouterContext_Truncation(t *testing.T) {
	longText := ""
	for i := 0; i < 200; i++ {
		longText += "x"
	}
	msgs := []Message{
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: longText}}},
	}
	result := BuildRouterContext(msgs, 3)
	if !strContains(result, "...") {
		t.Error("expected truncation marker '...'")
	}
	// Should not contain the full 200-char text.
	if strContains(result, longText) {
		t.Error("expected text to be truncated")
	}
}

func TestBuildRouterContext_MaxTurns(t *testing.T) {
	msgs := []Message{
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "msg1"}}},
		{Role: "assistant", Tier: "haiku", Blocks: []ContentBlock{{Type: BlockText, Text: "resp1"}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "msg2"}}},
		{Role: "assistant", Tier: "sonnet", Blocks: []ContentBlock{{Type: BlockText, Text: "resp2"}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "msg3"}}},
		{Role: "assistant", Tier: "haiku", Blocks: []ContentBlock{{Type: BlockText, Text: "resp3"}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "msg4"}}},
		{Role: "assistant", Tier: "sonnet", Blocks: []ContentBlock{{Type: BlockText, Text: "resp4"}}},
	}
	// maxTurns=2 → last 4 messages only.
	result := BuildRouterContext(msgs, 2)
	if strContains(result, "msg1") || strContains(result, "resp1") {
		t.Error("expected old messages to be excluded")
	}
	if !strContains(result, "msg3") || !strContains(result, "resp4") {
		t.Errorf("expected recent messages, got:\n%s", result)
	}
}

func TestBuildRouterContext_SkipsEmptyText(t *testing.T) {
	msgs := []Message{
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hello"}}},
		{Role: "assistant", Blocks: []ContentBlock{{Type: BlockToolUse}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "world"}}},
	}
	result := BuildRouterContext(msgs, 3)
	if strContains(result, "[assistant]") {
		t.Errorf("tool-only assistant message should be skipped, got:\n%s", result)
	}
	if !strContains(result, "hello") || !strContains(result, "world") {
		t.Errorf("expected user messages, got:\n%s", result)
	}
}

func TestBuildRouterContext_TierInLabel(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Tier: "opus", Blocks: []ContentBlock{{Type: BlockText, Text: "deep analysis"}}},
	}
	result := BuildRouterContext(msgs, 3)
	if !strContains(result, "[opus]") {
		t.Errorf("expected [opus] label, got %q", result)
	}
}

func strContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
