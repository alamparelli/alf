package conversation

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

// BlockType identifies the kind of content in a ContentBlock.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockThinking   BlockType = "thinking"
	BlockSummary    BlockType = "summary"
)

// RoleSummary marks a Message as a condensed summary of earlier messages.
// Its Blocks contain a single BlockSummary block; CoveredIDs lists which
// message IDs this summary replaces in the context window.
const RoleSummary = "summary"

// ContentBlock represents a single piece of content within a message.
// Modeled after Claude's streaming content blocks.
type ContentBlock struct {
	Type   BlockType `json:"type"`
	Text   string    `json:"text,omitempty"`    // text content or thinking content
	Name   string    `json:"name,omitempty"`    // tool name (tool_use)
	Input  string    `json:"input,omitempty"`   // tool input JSON (tool_use)
	ToolID string    `json:"tool_id,omitempty"` // links use <-> result
	Output string    `json:"output,omitempty"`  // tool output (tool_result)
}

// MediaRef references an uploaded media file.
type MediaRef struct {
	UploadID string `json:"upload_id"`
	Type     string `json:"type"`      // photo, document, video, voice
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	URL      string `json:"url,omitempty"`
}

// Reaction represents an emoji reaction on a message.
type Reaction struct {
	Emoji string `json:"emoji"`
	From  string `json:"from"` // "user" | "alf"
}

// Channel constants identify the frontend that originated a message.
const (
	ChannelTelegram = "tg"
	ChannelCC       = "cc"
)

// Message is a rich conversation message with full content blocks.
type Message struct {
	ID        string         `json:"id"`
	ConvID    string         `json:"conv_id,omitempty"` // conversation scope; messages with different ConvIDs are separate conversations
	Channel   string         `json:"channel,omitempty"` // originating frontend: "tg", "cc"
	Role      string         `json:"role"`              // user, assistant
	Blocks    []ContentBlock `json:"blocks"`
	Timestamp time.Time      `json:"ts"`
	Model     string         `json:"model,omitempty"`
	Tier      string         `json:"tier,omitempty"`
	Backend   string         `json:"backend,omitempty"`
	CostUSD   float64        `json:"cost_usd,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	ReplyTo   string         `json:"reply_to,omitempty"`
	Media     []MediaRef     `json:"media,omitempty"`
	Reactions []Reaction     `json:"reactions,omitempty"`

	// CoveredIDs lists the message IDs that this message replaces in the
	// context window. Only set for Role == RoleSummary. Readers filter out
	// any message whose ID appears here.
	CoveredIDs []string `json:"covered_ids,omitempty"`
}

// NewConvID generates a new conversation ID.
func NewConvID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("conv-%x", b)
}

// TextContent returns the concatenated text from all text blocks.
func (m *Message) TextContent() string {
	var parts []string
	for _, b := range m.Blocks {
		if b.Type == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "")
}

// NewMessageID generates a unique message ID.
func NewMessageID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Truncation limits for context building.
const (
	MaxToolResultBytes  = 2048
	MaxThinkingBytes    = 1024
	DefaultMaxMessages  = 50
)

// BuildRouterContext creates a compact conversation summary for the router
// classifier. Returns the last few user/assistant exchanges truncated to
// keep the classify prompt small. Returns "" if no relevant messages.
func BuildRouterContext(msgs []Message, maxTurns int) string {
	if len(msgs) == 0 {
		return ""
	}
	// Take the last maxTurns*2 messages (user+assistant pairs).
	start := 0
	if len(msgs) > maxTurns*2 {
		start = len(msgs) - maxTurns*2
	}
	var b strings.Builder
	for _, m := range msgs[start:] {
		text := m.TextContent()
		if text == "" {
			continue
		}
		// Truncate each turn to keep the summary compact.
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
