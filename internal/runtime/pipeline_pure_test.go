package runtime

import (
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
)

func TestStripToolXML_RemovesFunctionCalls(t *testing.T) {
	in := "Hello <function_calls>\n<invoke>\nignore me\n</invoke>\n</function_calls> world"
	got := stripToolXML(in)
	if got != "Hello  world" {
		t.Errorf("expected outer text preserved, got %q", got)
	}
}

func TestStripToolXML_RemovesInvoke(t *testing.T) {
	in := "intro <invoke>stuff</invoke> outro"
	got := stripToolXML(in)
	if got != "intro  outro" {
		t.Errorf("got %q", got)
	}
}

func TestStripToolXML_RemovesToolUse(t *testing.T) {
	in := "a <tool_use>x\ny</tool_use> b"
	got := stripToolXML(in)
	if got != "a  b" {
		t.Errorf("got %q", got)
	}
}

func TestStripToolXML_NoMatch(t *testing.T) {
	in := "  plain text  "
	got := stripToolXML(in)
	if got != "plain text" {
		t.Errorf("expected trimmed plain text, got %q", got)
	}
}

func TestBuildSummarizationPrompt_IncludesMessages(t *testing.T) {
	msgs := []memory.Message{
		{Role: "user", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "hello bot"}}},
		{Role: "assistant", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "hi human"}}},
	}
	prompt := buildSummarizationPrompt(msgs)

	for _, needle := range []string{"hello bot", "hi human", "[user]", "[assistant]", "=== conversation ===", "=== end ==="} {
		if !strings.Contains(prompt, needle) {
			t.Errorf("prompt missing %q: %s", needle, prompt)
		}
	}
}

func TestBuildSummarizationPrompt_SkipsEmptyText(t *testing.T) {
	msgs := []memory.Message{
		{Role: "user", Blocks: nil},
		{Role: "assistant", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "only me"}}},
	}
	prompt := buildSummarizationPrompt(msgs)
	if strings.Count(prompt, "[user]:") != 0 {
		t.Errorf("empty-text user message should be skipped: %s", prompt)
	}
	if !strings.Contains(prompt, "only me") {
		t.Errorf("assistant line missing: %s", prompt)
	}
}

func TestBuildSummarizationPrompt_DefaultRole(t *testing.T) {
	msgs := []memory.Message{
		{Role: "", Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "unknown role"}}},
	}
	prompt := buildSummarizationPrompt(msgs)
	if !strings.Contains(prompt, "[user]: unknown role") {
		t.Errorf("empty role should default to 'user': %s", prompt)
	}
}
