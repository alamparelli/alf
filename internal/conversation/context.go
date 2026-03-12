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
				// Drop thinking blocks entirely — they're internal.
				continue
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

// APIMessage is a simple role+content pair for API providers.
type APIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// FormatAsSystemPrompt renders conversation history as a system prompt
// injection for CLI providers that don't support message arrays.
func FormatAsSystemPrompt(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("=== [conversation history] ===\n")
	sb.WriteString("Previous messages in this conversation (for context continuity):\n\n")

	for _, m := range messages {
		role := m.Role
		if m.Tier != "" {
			role = fmt.Sprintf("%s [%s]", m.Role, m.Tier)
		}
		sb.WriteString(fmt.Sprintf("--- %s ---\n", role))

		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText:
				sb.WriteString(b.Text)
				sb.WriteString("\n")
			case BlockToolUse:
				sb.WriteString(fmt.Sprintf("[Used tool: %s]\n", b.Name))
			case BlockToolResult:
				output := b.Output
				if len(output) > 200 {
					output = output[:200] + "..."
				}
				sb.WriteString(fmt.Sprintf("[Tool result: %s]\n", output))
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
