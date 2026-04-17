package conversation

import (
	"fmt"
	"strings"
)

// BuildContext returns up to maxMessages recent messages, with truncation
// applied to reduce token cost. Drops thinking blocks first, then truncates
// tool_result output from oldest messages.
func BuildContext(messages []Message, maxMessages int) []Message {
	if maxMessages <= 0 {
		maxMessages = DefaultMaxMessages
	}

	// Take last N messages.
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}

	// Copy to avoid mutating the originals.
	result := make([]Message, len(messages))
	for i, m := range messages {
		result[i] = Message{
			ID:        m.ID,
			Role:      m.Role,
			Timestamp: m.Timestamp,
			Model:     m.Model,
			Tier:      m.Tier,
			Backend:   m.Backend,
			CostUSD:   m.CostUSD,
			SessionID: m.SessionID,
			ReplyTo:   m.ReplyTo,
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockThinking:
				// Drop thinking blocks entirely - they're internal.
				continue
			case BlockSummary:
				// Keep summary as-is; it's a condensed stand-in for older messages.
				result[i].Blocks = append(result[i].Blocks, b)
			case BlockToolResult:
				// Truncate old tool results.
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
// suitable for OpenAI-compatible API calls.
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
		result = append(result, APIMessage{
			Role:    m.Role,
			Content: text,
		})
	}
	return result
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

// APIMessage is a simple role+content pair for API providers.
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

// OpenAIMessage is a structured message for OpenAI-compatible APIs.
// Unlike APIMessage, it preserves tool_calls and tool results as structured
// data so models don't learn to simulate tool calls from text patterns.
type OpenAIMessage struct {
	Role       string           // "user", "assistant", "tool"
	Content    string           // text content
	ToolCalls  []OpenAIToolCall // assistant messages: tool invocations
	ToolCallID string           // tool messages: links result to its call
}

// FlattenForOpenAI converts rich messages into structured OpenAI-format
// messages that preserve tool calls and results as proper API structures.
// This prevents weaker models from learning to hallucinate tool calls
// by mimicking text patterns like "[Used tool: X]" in conversation history.
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
			// Assistant message with tool calls.
			msg := OpenAIMessage{Role: "assistant", ToolCalls: toolCalls}
			if len(textParts) > 0 {
				msg.Content = strings.Join(textParts, "\n")
			}
			result = append(result, msg)

			// Each tool result becomes a separate "tool" message.
			for _, tr := range toolResults {
				result = append(result, OpenAIMessage{
					Role:       "tool",
					Content:    tr.output,
					ToolCallID: tr.id,
				})
			}
		} else if len(textParts) > 0 {
			// Pure text assistant message.
			result = append(result, OpenAIMessage{
				Role:    "assistant",
				Content: strings.Join(textParts, "\n"),
			})
		}
	}
	return result
}

// FlattenTextOnly converts messages to plain text OpenAI messages, stripping
// all tool_use and tool_result blocks. Use this when switching backends to
// avoid sending tool call messages that the new backend didn't initiate.
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

// textFromBlocks extracts only text content from blocks.
func textFromBlocks(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// FormatAsSystemPrompt renders conversation history as a system prompt
// injection for CLI providers that don't support message arrays.
// contextWeight controls verbosity: "light" strips tool blocks, "standard"/"full" include them.
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
				// Light tiers: skip tool noise to maximize conversation signal.
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

// flattenBlocks combines all blocks into a single text representation.
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
