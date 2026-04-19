package memory

import (
	"fmt"
	"strings"
)

// Truncation limits shared by BuildContext and the flatteners. Originated in
// internal/conversation; kept here so consumers don't have to reach across
// packages to know why a tool_result got truncated.
const (
	MaxToolResultBytes = 2048
	MaxThinkingBytes   = 1024
	DefaultMaxMessages = 50
)

// APIMessage is a simple role+content pair for provider APIs that don't
// support structured content blocks.
type APIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIToolCall represents a tool invocation in OpenAI message format.
type OpenAIToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// OpenAIMessage is a structured message for OpenAI-compatible APIs. Unlike
// APIMessage it preserves tool_calls and tool results as structured data so
// models don't learn to simulate tool calls from text patterns.
type OpenAIMessage struct {
	Role       string           // "user", "assistant", "tool"
	Content    string           // text content
	ToolCalls  []OpenAIToolCall // assistant messages: tool invocations
	ToolCallID string           // tool messages: links result to its call
}

// BuildContext returns up to maxMessages recent messages, with truncation
// applied to reduce token cost. Drops thinking blocks first, then truncates
// tool_result output from oldest messages. The input slice is not mutated.
func BuildContext(messages []Message, maxMessages int) []Message {
	if maxMessages <= 0 {
		maxMessages = DefaultMaxMessages
	}
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
	result := make([]Message, len(messages))
	for i, m := range messages {
		result[i] = Message{
			ID:         m.ID,
			Seq:        m.Seq,
			Role:       m.Role,
			Channel:    m.Channel,
			Content:    m.Content,
			Model:      m.Model,
			Tier:       m.Tier,
			Backend:    m.Backend,
			CostUSD:    m.CostUSD,
			DurationMs: m.DurationMs,
			SessionID:  m.SessionID,
			ReplyTo:    m.ReplyTo,
			CoveredIDs: m.CoveredIDs,
			CreatedAt:  m.CreatedAt,
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockThinking:
				// Thinking blocks are internal — drop.
				continue
			case BlockSummary:
				// Summary stays as-is; it's a condensed stand-in for older messages.
				result[i].Blocks = append(result[i].Blocks, b)
			case BlockToolResult:
				output := b.Output
				if len(output) > MaxToolResultBytes {
					output = output[:MaxToolResultBytes] + "..."
				}
				result[i].Blocks = append(result[i].Blocks, ContentBlock{
					Type:   b.Type,
					ToolID: b.ToolID,
					Output: output,
				})
			default:
				result[i].Blocks = append(result[i].Blocks, b)
			}
		}
	}
	return result
}

// FlattenForAPI converts rich messages into simple role+content pairs
// suitable for OpenAI-compatible API calls that don't preserve tool
// structures. Summary messages become a "system" role prefixed with a
// marker so the model recognises condensed history.
func FlattenForAPI(messages []Message) []APIMessage {
	var result []APIMessage
	for _, m := range messages {
		if m.Role == RoleSummary {
			text := summaryText(m.Blocks)
			if text == "" {
				continue
			}
			result = append(result, APIMessage{
				Role:    "system",
				Content: "Summary of earlier conversation:\n" + text,
			})
			continue
		}
		text := flattenBlocks(m.Blocks)
		if text == "" {
			continue
		}
		result = append(result, APIMessage{Role: m.Role, Content: text})
	}
	return result
}

// FlattenForOpenAI converts rich messages into structured OpenAI-format
// messages that preserve tool calls and results as proper API structures.
// Prevents weaker models from learning to hallucinate tool calls by mimicking
// "[Used tool: X]" text patterns.
func FlattenForOpenAI(messages []Message) []OpenAIMessage {
	var result []OpenAIMessage
	for _, m := range messages {
		if m.Role == RoleSummary {
			text := summaryText(m.Blocks)
			if text != "" {
				result = append(result, OpenAIMessage{
					Role:    "system",
					Content: "Summary of earlier conversation:\n" + text,
				})
			}
			continue
		}
		if m.Role == "user" {
			text := textFromBlocks(m.Blocks)
			if text != "" {
				result = append(result, OpenAIMessage{Role: "user", Content: text})
			}
			continue
		}

		// Assistant messages: split into structured parts.
		var textParts []string
		var toolCalls []OpenAIToolCall
		var toolResults []struct{ id, output string }

		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText:
				if b.Text != "" {
					textParts = append(textParts, b.Text)
				}
			case BlockToolUse:
				args := b.Input
				if args == "" {
					args = "{}"
				}
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:        b.ToolID,
					Name:      b.Name,
					Arguments: args,
				})
			case BlockToolResult:
				output := b.Output
				if len(output) > MaxToolResultBytes {
					output = output[:MaxToolResultBytes] + "..."
				}
				toolResults = append(toolResults, struct{ id, output string }{b.ToolID, output})
			}
		}

		if len(toolCalls) > 0 {
			msg := OpenAIMessage{Role: "assistant", ToolCalls: toolCalls}
			if len(textParts) > 0 {
				msg.Content = strings.Join(textParts, "\n")
			}
			result = append(result, msg)
			for _, tr := range toolResults {
				result = append(result, OpenAIMessage{
					Role:       "tool",
					Content:    tr.output,
					ToolCallID: tr.id,
				})
			}
		} else if len(textParts) > 0 {
			result = append(result, OpenAIMessage{
				Role:    "assistant",
				Content: strings.Join(textParts, "\n"),
			})
		}
	}
	return result
}

// FlattenTextOnly converts messages to plain text OpenAI messages, stripping
// tool_use and tool_result blocks. Use when switching backends to avoid
// sending tool call messages that the new backend didn't initiate.
func FlattenTextOnly(messages []Message) []OpenAIMessage {
	var result []OpenAIMessage
	for _, m := range messages {
		if m.Role == RoleSummary {
			text := summaryText(m.Blocks)
			if text != "" {
				result = append(result, OpenAIMessage{
					Role:    "system",
					Content: "Summary of earlier conversation:\n" + text,
				})
			}
			continue
		}
		text := textFromBlocks(m.Blocks)
		if text == "" {
			continue
		}
		role := m.Role
		if role == "tool" {
			role = "user"
		}
		result = append(result, OpenAIMessage{Role: role, Content: text})
	}
	return result
}

// FormatAsSystemPrompt renders conversation history as a system-prompt
// injection for CLI-only providers that don't support message arrays.
// contextWeight controls verbosity: "light" strips tool blocks,
// "standard"/"full" include them.
func FormatAsSystemPrompt(messages []Message, contextWeight ...string) string {
	if len(messages) == 0 {
		return ""
	}
	weight := "full"
	if len(contextWeight) > 0 && contextWeight[0] != "" {
		weight = contextWeight[0]
	}

	var sb strings.Builder
	sb.WriteString("=== [conversation history] ===\n")
	sb.WriteString("Previous messages in this conversation (for context continuity):\n\n")

	for _, m := range messages {
		role := m.Role
		if m.Tier != "" {
			role = fmt.Sprintf("%s [%s]", m.Role, m.Tier)
		}
		if m.Role == RoleSummary {
			sb.WriteString("--- summary of earlier conversation ---\n")
			for _, b := range m.Blocks {
				if b.Type == BlockSummary {
					sb.WriteString(b.Text)
					sb.WriteString("\n")
				}
			}
			sb.WriteString("\n")
			continue
		}
		sb.WriteString(fmt.Sprintf("--- %s ---\n", role))
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText:
				sb.WriteString(b.Text)
				sb.WriteString("\n")
			case BlockToolUse:
				if weight != "light" {
					sb.WriteString(fmt.Sprintf("[Used tool: %s]\n", b.Name))
				}
			case BlockToolResult:
				if weight != "light" {
					output := b.Output
					if len(output) > 200 {
						output = output[:200] + "..."
					}
					sb.WriteString(fmt.Sprintf("[Tool result: %s]\n", output))
				}
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("=== [end conversation history] ===")
	return sb.String()
}

// BuildRouterContext creates a compact conversation summary for the router
// classifier. Returns the last maxTurns*2 messages truncated so the classify
// prompt stays small. Returns "" if no relevant messages.
func BuildRouterContext(msgs []Message, maxTurns int) string {
	if len(msgs) == 0 {
		return ""
	}
	start := 0
	if len(msgs) > maxTurns*2 {
		start = len(msgs) - maxTurns*2
	}
	var b strings.Builder
	for _, m := range msgs[start:] {
		text := TextContent(m)
		if text == "" {
			continue
		}
		if len(text) > 150 {
			text = text[:150] + "..."
		}
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
			if m.Tier != "" {
				role = m.Tier
			}
		}
		b.WriteString(fmt.Sprintf("[%s]: %s\n", role, text))
	}
	return b.String()
}

// TextContent returns the concatenated text from all BlockText blocks in m.
// Falls back to m.Content when Blocks is empty — keeps callers that use
// AppendMessage with a plain Content string working.
func TextContent(m Message) string {
	if len(m.Blocks) == 0 {
		return m.Content
	}
	var parts []string
	for _, b := range m.Blocks {
		if b.Type == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "")
}

func flattenBlocks(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case BlockToolUse:
			parts = append(parts, fmt.Sprintf("[Used tool: %s]", b.Name))
		case BlockToolResult:
			output := b.Output
			if len(output) > 500 {
				output = output[:500] + "..."
			}
			if output != "" {
				parts = append(parts, fmt.Sprintf("[Tool result: %s]", output))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func textFromBlocks(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func summaryText(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockSummary && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
