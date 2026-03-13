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

func TestFlattenForOpenAI_UserOnly(t *testing.T) {
	msgs := []Message{
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hello"}}},
	}
	result := FlattenForOpenAI(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "hello" {
		t.Errorf("unexpected result: %+v", result[0])
	}
}

func TestFlattenForOpenAI_AssistantTextOnly(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Blocks: []ContentBlock{{Type: BlockText, Text: "answer"}}},
	}
	result := FlattenForOpenAI(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "assistant" || result[0].Content != "answer" {
		t.Errorf("unexpected result: %+v", result[0])
	}
	if len(result[0].ToolCalls) > 0 {
		t.Error("unexpected tool calls in text-only message")
	}
}

func TestFlattenForOpenAI_ToolCalls(t *testing.T) {
	msgs := []Message{
		{
			Role: "assistant",
			Blocks: []ContentBlock{
				{Type: BlockText, Text: "Let me check."},
				{Type: BlockToolUse, ToolID: "call_1", Name: "read_file", Input: `{"path":"test.go"}`},
				{Type: BlockToolResult, ToolID: "call_1", Output: "file contents here"},
			},
		},
	}
	result := FlattenForOpenAI(msgs)

	// Should produce: 1 assistant message with tool_calls + 1 tool result message.
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	assistant := result[0]
	if assistant.Role != "assistant" {
		t.Errorf("expected assistant role, got %s", assistant.Role)
	}
	if assistant.Content != "Let me check." {
		t.Errorf("expected text content, got %q", assistant.Content)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].Name != "read_file" {
		t.Errorf("expected read_file, got %s", assistant.ToolCalls[0].Name)
	}
	if assistant.ToolCalls[0].ID != "call_1" {
		t.Errorf("expected call_1, got %s", assistant.ToolCalls[0].ID)
	}

	toolMsg := result[1]
	if toolMsg.Role != "tool" {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_1" {
		t.Errorf("expected call_1 tool call ID, got %s", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "file contents here" {
		t.Errorf("expected tool output, got %q", toolMsg.Content)
	}
}

func TestFlattenForOpenAI_MultipleToolCalls(t *testing.T) {
	msgs := []Message{
		{
			Role: "assistant",
			Blocks: []ContentBlock{
				{Type: BlockToolUse, ToolID: "c1", Name: "grep", Input: `{"pattern":"foo"}`},
				{Type: BlockToolResult, ToolID: "c1", Output: "match1"},
				{Type: BlockToolUse, ToolID: "c2", Name: "glob", Input: `{"pattern":"*.go"}`},
				{Type: BlockToolResult, ToolID: "c2", Output: "match2"},
			},
		},
	}
	result := FlattenForOpenAI(msgs)

	// 1 assistant with 2 tool_calls + 2 tool results.
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if len(result[0].ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(result[0].ToolCalls))
	}
	if result[1].ToolCallID != "c1" || result[2].ToolCallID != "c2" {
		t.Error("tool call IDs don't match")
	}
}

func TestFlattenForOpenAI_EmptyToolUseArgs(t *testing.T) {
	msgs := []Message{
		{
			Role: "assistant",
			Blocks: []ContentBlock{
				{Type: BlockToolUse, ToolID: "c1", Name: "list", Input: ""},
				{Type: BlockToolResult, ToolID: "c1", Output: "ok"},
			},
		},
	}
	result := FlattenForOpenAI(msgs)
	if result[0].ToolCalls[0].Arguments != "{}" {
		t.Errorf("expected empty args to default to {}, got %q", result[0].ToolCalls[0].Arguments)
	}
}

func TestFlattenForOpenAI_SkipsEmptyMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Blocks: nil},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "real"}}},
	}
	result := FlattenForOpenAI(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message (empty skipped), got %d", len(result))
	}
}

func TestFlattenForOpenAI_LargeToolResultTruncated(t *testing.T) {
	bigOutput := strings.Repeat("x", MaxToolResultBytes+100)
	msgs := []Message{
		{
			Role: "assistant",
			Blocks: []ContentBlock{
				{Type: BlockToolUse, ToolID: "c1", Name: "bash", Input: "{}"},
				{Type: BlockToolResult, ToolID: "c1", Output: bigOutput},
			},
		},
	}
	result := FlattenForOpenAI(msgs)
	toolMsg := result[1]
	if len(toolMsg.Content) > MaxToolResultBytes+10 {
		t.Errorf("expected truncated output, got length %d", len(toolMsg.Content))
	}
	if !strings.HasSuffix(toolMsg.Content, "...") {
		t.Error("expected truncation suffix '...'")
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
