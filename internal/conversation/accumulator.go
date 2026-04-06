package conversation

import (
	"strings"
	"sync"

	"github.com/alamparelli/alf/internal/provider"
)

// Accumulator captures content blocks from StreamEvent callbacks during
// a provider invocation. After invocation completes, call Blocks() to
// get the captured content blocks for the assistant message.
type Accumulator struct {
	mu     sync.Mutex
	blocks []ContentBlock

	// State tracking for in-progress blocks.
	curText      strings.Builder
	curThinking  strings.Builder
	curToolName  string
	curToolID    string
	curToolInput strings.Builder
	inThinking   bool
	inText       bool
}

// NewAccumulator creates a new Accumulator.
func NewAccumulator() *Accumulator {
	return &Accumulator{}
}

// OnProgress returns an OnProgress callback that captures blocks and
// delegates to the optional inner callback. Use this to wrap existing
// progress handlers.
func (a *Accumulator) OnProgress(inner provider.OnProgress) provider.OnProgress {
	return func(event provider.StreamEvent) {
		a.processEvent(event)
		if inner != nil {
			inner(event)
		}
	}
}

func (a *Accumulator) processEvent(event provider.StreamEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch event.Type {
	case "thinking":
		if event.Text != "" {
			// Thinking delta - accumulate.
			if !a.inThinking {
				a.flushText()
				a.flushTool()
				a.inThinking = true
			}
			a.curThinking.WriteString(event.Text)
		} else {
			// Thinking block start (no text).
			a.flushText()
			a.flushTool()
			a.inThinking = true
		}

	case "text_delta":
		if a.inThinking {
			a.flushThinking()
		}
		a.flushTool()
		if !a.inText {
			a.inText = true
		}
		a.curText.WriteString(event.Text)

	case "tool_use":
		// New tool_use block starting — flush any pending tool first.
		a.flushThinking()
		a.flushText()
		a.flushTool()
		a.curToolName = event.Detail
		a.curToolID = ""
		a.curToolInput.Reset()

	case "tool_input":
		// Partial JSON input for the current tool.
		a.curToolInput.WriteString(event.Text)

	case "tool_result":
		// Tool result - finalize the tool_use block and add tool_result.
		toolID := event.Detail
		if a.curToolName != "" {
			if a.curToolID == "" {
				a.curToolID = toolID
			}
			a.blocks = append(a.blocks, ContentBlock{
				Type:   BlockToolUse,
				Name:   a.curToolName,
				Input:  a.curToolInput.String(),
				ToolID: a.curToolID,
			})
			a.curToolName = ""
			a.curToolInput.Reset()
		}
		output := event.Text
		if len(output) > MaxToolResultBytes {
			output = output[:MaxToolResultBytes] + "..."
		}
		a.blocks = append(a.blocks, ContentBlock{
			Type:   BlockToolResult,
			ToolID: toolID,
			Output: output,
		})

	case "block_stop":
		// Generic block stop - flush any pending state.
		a.flushThinking()
		a.flushText()
	}
}

// flushThinking finalizes any accumulated thinking content.
func (a *Accumulator) flushThinking() {
	if !a.inThinking {
		return
	}
	text := a.curThinking.String()
	if text != "" {
		if len(text) > MaxThinkingBytes {
			text = text[:MaxThinkingBytes] + "..."
		}
		a.blocks = append(a.blocks, ContentBlock{
			Type: BlockThinking,
			Text: text,
		})
	}
	a.curThinking.Reset()
	a.inThinking = false
}

// flushText finalizes any accumulated text content.
func (a *Accumulator) flushText() {
	if !a.inText {
		return
	}
	text := a.curText.String()
	if text != "" {
		a.blocks = append(a.blocks, ContentBlock{
			Type: BlockText,
			Text: text,
		})
	}
	a.curText.Reset()
	a.inText = false
}

// flushTool finalizes any pending tool_use block.
func (a *Accumulator) flushTool() {
	if a.curToolName != "" {
		a.blocks = append(a.blocks, ContentBlock{
			Type:   BlockToolUse,
			Name:   a.curToolName,
			Input:  a.curToolInput.String(),
			ToolID: a.curToolID,
		})
		a.curToolName = ""
		a.curToolInput.Reset()
	}
}

// Blocks returns the captured content blocks and flushes any pending state.
// Call this after the provider invocation completes.
func (a *Accumulator) Blocks() []ContentBlock {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.flushThinking()
	a.flushText()
	a.flushTool()

	result := make([]ContentBlock, len(a.blocks))
	copy(result, a.blocks)
	return result
}
