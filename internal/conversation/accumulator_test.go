package conversation

import (
	"testing"

	"github.com/alamparelli/alf/internal/provider"
)

func TestAccumulatorTextOnly(t *testing.T) {
	acc := NewAccumulator()
	cb := acc.OnProgress(nil)

	cb(provider.StreamEvent{Type: "text_delta", Text: "Hello "})
	cb(provider.StreamEvent{Type: "text_delta", Text: "world"})

	blocks := acc.Blocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != BlockText {
		t.Errorf("expected text block, got %s", blocks[0].Type)
	}
	if blocks[0].Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", blocks[0].Text)
	}
}

func TestAccumulatorThinkingThenText(t *testing.T) {
	acc := NewAccumulator()
	cb := acc.OnProgress(nil)

	cb(provider.StreamEvent{Type: "thinking"})
	cb(provider.StreamEvent{Type: "thinking", Text: "Let me think about this"})
	cb(provider.StreamEvent{Type: "text_delta", Text: "The answer is 42"})

	blocks := acc.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Type != BlockThinking {
		t.Errorf("expected thinking block, got %s", blocks[0].Type)
	}
	if blocks[1].Type != BlockText {
		t.Errorf("expected text block, got %s", blocks[1].Type)
	}
}

func TestAccumulatorToolUseAndResult(t *testing.T) {
	acc := NewAccumulator()
	cb := acc.OnProgress(nil)

	cb(provider.StreamEvent{Type: "thinking"})
	cb(provider.StreamEvent{Type: "thinking", Text: "I need to read a file"})
	cb(provider.StreamEvent{Type: "tool_use", Detail: "Read"})
	cb(provider.StreamEvent{Type: "tool_input", Detail: "Read", Text: `{"path":"/foo"}`})
	cb(provider.StreamEvent{Type: "tool_result", Detail: "tool-123", Text: "file contents here"})
	cb(provider.StreamEvent{Type: "text_delta", Text: "I found the answer"})

	blocks := acc.Blocks()
	if len(blocks) != 4 {
		t.Fatalf("expected 4 blocks (thinking, tool_use, tool_result, text), got %d", len(blocks))
	}

	if blocks[0].Type != BlockThinking {
		t.Errorf("block 0: expected thinking, got %s", blocks[0].Type)
	}
	if blocks[1].Type != BlockToolUse {
		t.Errorf("block 1: expected tool_use, got %s", blocks[1].Type)
	}
	if blocks[1].Name != "Read" {
		t.Errorf("block 1: expected tool name 'Read', got %q", blocks[1].Name)
	}
	if blocks[1].Input != `{"path":"/foo"}` {
		t.Errorf("block 1: unexpected input %q", blocks[1].Input)
	}
	if blocks[2].Type != BlockToolResult {
		t.Errorf("block 2: expected tool_result, got %s", blocks[2].Type)
	}
	if blocks[2].Output != "file contents here" {
		t.Errorf("block 2: unexpected output %q", blocks[2].Output)
	}
	if blocks[3].Type != BlockText {
		t.Errorf("block 3: expected text, got %s", blocks[3].Type)
	}
}

func TestAccumulatorDelegatesToInner(t *testing.T) {
	acc := NewAccumulator()
	var called int
	inner := func(event provider.StreamEvent) {
		called++
	}
	cb := acc.OnProgress(inner)

	cb(provider.StreamEvent{Type: "text_delta", Text: "hi"})
	cb(provider.StreamEvent{Type: "thinking", Text: "hmm"})

	if called != 2 {
		t.Errorf("expected inner called 2 times, got %d", called)
	}
}

func TestAccumulatorToolResultTruncation(t *testing.T) {
	acc := NewAccumulator()
	cb := acc.OnProgress(nil)

	// Generate a large tool result.
	bigOutput := make([]byte, MaxToolResultBytes+500)
	for i := range bigOutput {
		bigOutput[i] = 'x'
	}

	cb(provider.StreamEvent{Type: "tool_use", Detail: "Bash"})
	cb(provider.StreamEvent{Type: "tool_result", Detail: "t1", Text: string(bigOutput)})

	blocks := acc.Blocks()
	var resultBlock *ContentBlock
	for _, b := range blocks {
		if b.Type == BlockToolResult {
			resultBlock = &b
			break
		}
	}
	if resultBlock == nil {
		t.Fatal("expected a tool_result block")
	}
	if len(resultBlock.Output) > MaxToolResultBytes+10 {
		t.Errorf("tool result should be truncated, got %d bytes", len(resultBlock.Output))
	}
}
