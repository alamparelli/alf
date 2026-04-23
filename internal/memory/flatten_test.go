package memory_test

import (
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
)

func TestBuildContext_DropsThinking(t *testing.T) {
	msgs := []memory.Message{{
		ID:   "m1",
		Role: "assistant",
		Blocks: []memory.ContentBlock{
			{Type: memory.BlockThinking, Text: "secret thoughts"},
			{Type: memory.BlockText, Text: "visible"},
		},
	}}
	out := memory.BuildContext(msgs, 50)
	if len(out) != 1 {
		t.Fatalf("want 1 msg, got %d", len(out))
	}
	for _, b := range out[0].Blocks {
		if b.Type == memory.BlockThinking {
			t.Error("thinking block leaked into context")
		}
	}
	if len(out[0].Blocks) != 1 || out[0].Blocks[0].Text != "visible" {
		t.Errorf("want only [text: visible], got %+v", out[0].Blocks)
	}
}

func TestBuildContext_LimitsMessages(t *testing.T) {
	var msgs []memory.Message
	for i := 0; i < 100; i++ {
		msgs = append(msgs, memory.Message{Role: "user", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "x"}}})
	}
	out := memory.BuildContext(msgs, 10)
	if len(out) != 10 {
		t.Errorf("want 10, got %d", len(out))
	}
}

func TestBuildContext_TruncatesToolResult(t *testing.T) {
	big := strings.Repeat("a", memory.MaxToolResultBytes*2)
	msgs := []memory.Message{{
		Role: "assistant",
		Blocks: []memory.ContentBlock{
			{Type: memory.BlockToolResult, Output: big, ToolID: "t1"},
		},
	}}
	out := memory.BuildContext(msgs, 50)
	got := out[0].Blocks[0].Output
	if len(got) > memory.MaxToolResultBytes+3 {
		t.Errorf("tool result not truncated: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated output should end in '...', got %q", got[len(got)-5:])
	}
}

func TestFlattenForAPI_ToolAndSummary(t *testing.T) {
	msgs := []memory.Message{
		{Role: "user", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "What is 2+2?"}}},
		{
			Role: "assistant",
			Blocks: []memory.ContentBlock{
				{Type: memory.BlockToolUse, Name: "Calculator"},
				{Type: memory.BlockToolResult, Output: "4"},
				{Type: memory.BlockText, Text: "The answer is 4."},
			},
		},
		{Role: memory.RoleSummary, Blocks: []memory.ContentBlock{{Type: memory.BlockSummary, Text: "math question"}}},
	}
	flat := memory.FlattenForAPI(msgs)
	if len(flat) != 3 {
		t.Fatalf("want 3 flattened, got %d", len(flat))
	}
	if flat[0].Role != "user" {
		t.Errorf("flat[0].Role = %q, want user", flat[0].Role)
	}
	if !strings.Contains(flat[1].Content, "The answer is 4") || !strings.Contains(flat[1].Content, "Calculator") {
		t.Errorf("assistant content missing tool/text: %q", flat[1].Content)
	}
	if flat[2].Role != "system" || !strings.Contains(flat[2].Content, "math question") {
		t.Errorf("summary should become system msg: %+v", flat[2])
	}
}

func TestFlattenForOpenAI_PreservesStructure(t *testing.T) {
	msgs := []memory.Message{
		{Role: "user", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "check file"}}},
		{
			Role: "assistant",
			Blocks: []memory.ContentBlock{
				{Type: memory.BlockText, Text: "reading"},
				{Type: memory.BlockToolUse, Name: "read_file", Input: `{"path":"x"}`, ToolID: "t1"},
				{Type: memory.BlockToolResult, Output: "contents", ToolID: "t1"},
			},
		},
	}
	flat := memory.FlattenForOpenAI(msgs)
	if len(flat) != 3 {
		t.Fatalf("want 3 (user, assistant+toolcalls, tool), got %d", len(flat))
	}
	if flat[1].Role != "assistant" || len(flat[1].ToolCalls) != 1 || flat[1].ToolCalls[0].Name != "read_file" {
		t.Errorf("assistant should carry tool_call: %+v", flat[1])
	}
	if flat[2].Role != "tool" || flat[2].ToolCallID != "t1" || flat[2].Content != "contents" {
		t.Errorf("tool result message malformed: %+v", flat[2])
	}
}

func TestFlattenTextOnly_StripsTools(t *testing.T) {
	msgs := []memory.Message{
		{Role: "assistant", Blocks: []memory.ContentBlock{
			{Type: memory.BlockText, Text: "hi"},
			{Type: memory.BlockToolUse, Name: "read_file"},
			{Type: memory.BlockToolResult, Output: "contents"},
		}},
	}
	flat := memory.FlattenTextOnly(msgs)
	if len(flat) != 1 {
		t.Fatalf("want 1 msg, got %d", len(flat))
	}
	if flat[0].Content != "hi" {
		t.Errorf("tools should be stripped, got %q", flat[0].Content)
	}
}

func TestFormatAsSystemPrompt_SummaryBlock(t *testing.T) {
	msgs := []memory.Message{
		{Role: "user", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "hello"}}},
		{Role: memory.RoleSummary, Blocks: []memory.ContentBlock{{Type: memory.BlockSummary, Text: "prior convo"}}},
	}
	out := memory.FormatAsSystemPrompt(msgs)
	if !strings.Contains(out, "hello") {
		t.Error("user msg missing from prompt")
	}
	if !strings.Contains(out, "summary of earlier conversation") || !strings.Contains(out, "prior convo") {
		t.Errorf("summary block not rendered: %s", out)
	}
}

func TestFormatAsSystemPrompt_LightDropsToolBlocks(t *testing.T) {
	msgs := []memory.Message{{Role: "assistant", Blocks: []memory.ContentBlock{
		{Type: memory.BlockToolUse, Name: "X"},
		{Type: memory.BlockToolResult, Output: "Y"},
		{Type: memory.BlockText, Text: "ok"},
	}}}
	light := memory.FormatAsSystemPrompt(msgs, "light")
	full := memory.FormatAsSystemPrompt(msgs, "full")
	if strings.Contains(light, "[Used tool:") {
		t.Error("light weight should omit tool_use")
	}
	if !strings.Contains(full, "[Used tool:") {
		t.Error("full weight should include tool_use")
	}
}

func TestBuildRouterContext_Truncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	msgs := []memory.Message{
		{Role: "user", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: long}}},
		{Role: "assistant", Tier: "hero", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "short reply"}}},
	}
	out := memory.BuildRouterContext(msgs, 2)
	if !strings.Contains(out, "[user]:") || !strings.Contains(out, "[hero]:") {
		t.Errorf("roles missing: %s", out)
	}
	if strings.Count(out, "xxx") > 0 && !strings.Contains(out, "...") {
		t.Error("long line should be truncated with ...")
	}
}

func TestTextContent_FallsBackToContent(t *testing.T) {
	// Blocks empty → use the Content field (covers AppendMessage with just Content).
	m := memory.Message{Role: "user", Content: "plain"}
	if got := memory.TextContent(m); got != "plain" {
		t.Errorf("TextContent fallback failed: %q", got)
	}
	// Blocks present → ignore Content.
	m2 := memory.Message{Role: "user", Content: "ignored", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "from block"}}}
	if got := memory.TextContent(m2); got != "from block" {
		t.Errorf("TextContent with blocks: got %q", got)
	}
}
