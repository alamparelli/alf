package conversation

import (
	"strings"
	"testing"
	"time"
)

func TestBuildContextDropsThinking(t *testing.T) {
	msgs := []Message{
		{
			ID:   "m1",
			Role: "assistant",
			Blocks: []ContentBlock{
				{Type: BlockThinking, Text: "secret thoughts"},
				{Type: BlockText, Text: "visible answer"},
			},
		},
	}

	ctx := BuildContext(msgs, 50)
	if len(ctx) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ctx))
	}
	for _, b := range ctx[0].Blocks {
		if b.Type == BlockThinking {
			t.Error("thinking blocks should be dropped")
		}
	}
	if len(ctx[0].Blocks) != 1 {
		t.Fatalf("expected 1 block after dropping thinking, got %d", len(ctx[0].Blocks))
	}
}

func TestBuildContextLimitsMessages(t *testing.T) {
	var msgs []Message
	for i := 0; i < 100; i++ {
		msgs = append(msgs, Message{
			ID:     NewMessageID(),
			Role:   "user",
			Blocks: []ContentBlock{{Type: BlockText, Text: "msg"}},
		})
	}

	ctx := BuildContext(msgs, 10)
	if len(ctx) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(ctx))
	}
}

func TestFlattenForAPI(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			Blocks: []ContentBlock{
				{Type: BlockText, Text: "What is 2+2?"},
			},
		},
		{
			Role: "assistant",
			Blocks: []ContentBlock{
				{Type: BlockToolUse, Name: "Calculator"},
				{Type: BlockToolResult, Output: "4"},
				{Type: BlockText, Text: "The answer is 4."},
			},
		},
	}

	flat := FlattenForAPI(msgs)
	if len(flat) != 2 {
		t.Fatalf("expected 2 API messages, got %d", len(flat))
	}
	if flat[0].Role != "user" {
		t.Errorf("expected user role, got %s", flat[0].Role)
	}
	if !strings.Contains(flat[1].Content, "The answer is 4") {
		t.Errorf("expected text content, got %q", flat[1].Content)
	}
	if !strings.Contains(flat[1].Content, "Calculator") {
		t.Errorf("expected tool reference in flattened content, got %q", flat[1].Content)
	}
}

func TestFormatAsSystemPrompt(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			Tier: "",
			Blocks: []ContentBlock{
				{Type: BlockText, Text: "Hello"},
			},
			Timestamp: time.Now(),
		},
		{
			Role: "assistant",
			Tier: "sonnet",
			Blocks: []ContentBlock{
				{Type: BlockText, Text: "Hi there!"},
			},
			Timestamp: time.Now(),
		},
	}

	prompt := FormatAsSystemPrompt(msgs)
	if prompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if !strings.Contains(prompt, "conversation history") {
		t.Error("expected 'conversation history' header")
	}
	if !strings.Contains(prompt, "Hello") {
		t.Error("expected user message text")
	}
	if !strings.Contains(prompt, "Hi there!") {
		t.Error("expected assistant message text")
	}
	if !strings.Contains(prompt, "[sonnet]") {
		t.Error("expected tier annotation")
	}
}

func TestFormatAsSystemPromptEmpty(t *testing.T) {
	prompt := FormatAsSystemPrompt(nil)
	if prompt != "" {
		t.Errorf("expected empty string for nil messages, got %q", prompt)
	}
}

func TestTextContent(t *testing.T) {
	m := Message{
		Blocks: []ContentBlock{
			{Type: BlockThinking, Text: "hmm"},
			{Type: BlockText, Text: "part 1"},
			{Type: BlockToolUse, Name: "Read"},
			{Type: BlockText, Text: "part 2"},
		},
	}
	text := m.TextContent()
	if text != "part 1part 2" {
		t.Errorf("expected 'part 1part 2', got %q", text)
	}
}
