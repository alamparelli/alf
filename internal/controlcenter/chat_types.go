package controlcenter

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/alamparelli/alf/internal/memory"
)

// HistoryMessage is the JSON-stable wire shape for GET /api/chat history.
// Fields and tags mirror the legacy chatdb.Message so the frontend contract
// is unchanged after the memory migration (#336).
type HistoryMessage struct {
	ID         string              `json:"id"`
	ConvID     string              `json:"conv_id"`
	Seq        int64               `json:"seq"`
	Role       string              `json:"role"`
	Text       string              `json:"text"`
	Source     string              `json:"source"`
	Model      string              `json:"model,omitempty"`
	Tier       string              `json:"tier,omitempty"`
	CostUSD    float64             `json:"cost_usd,omitempty"`
	DurationMs int64               `json:"duration_ms,omitempty"`
	SessionID  string              `json:"session_id,omitempty"`
	ReplyTo    string              `json:"reply_to,omitempty"`
	CreatedAt  time.Time           `json:"ts"`
	Blocks     []HistoryBlock      `json:"content_blocks,omitempty"`
	Reactions  []HistoryReaction   `json:"reactions,omitempty"`
	Media      []HistoryMediaRef   `json:"media,omitempty"`
}

// HistoryBlock mirrors the legacy chatdb.ContentBlock JSON.
type HistoryBlock struct {
	BlockIndex int    `json:"block_index"`
	BlockType  string `json:"type"`
	Text       string `json:"text,omitempty"`
	Name       string `json:"name,omitempty"`
	Input      string `json:"input,omitempty"`
	ToolID     string `json:"tool_id,omitempty"`
	Output     string `json:"output,omitempty"`
}

// HistoryReaction mirrors chatdb.Reaction.
type HistoryReaction struct {
	Emoji  string `json:"emoji"`
	Source string `json:"from"`
}

// HistoryMediaRef mirrors chatdb.MediaRef JSON (MediaType exposed as "type").
type HistoryMediaRef struct {
	UploadID  string `json:"upload_id"`
	FileName  string `json:"file_name"`
	MimeType  string `json:"mime_type"`
	MediaType string `json:"type"`
	URL       string `json:"url,omitempty"`
}

// ConversationHistory mirrors chatdb.ConversationInfo so the JSON contract
// remains stable after the memory migration.
type ConversationHistory struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastMessage time.Time `json:"last_message"`
	Archived    bool      `json:"archived"`
	MsgCount    int       `json:"msg_count"`
}

// memoryMessageToHistory projects a memory.Message onto the legacy JSON shape.
func memoryMessageToHistory(m memory.Message, convID string) HistoryMessage {
	blocks := make([]HistoryBlock, 0, len(m.Blocks))
	for i, b := range m.Blocks {
		blocks = append(blocks, HistoryBlock{
			BlockIndex: i,
			BlockType:  string(b.Type),
			Text:       b.Text,
			Name:       b.Name,
			Input:      b.Input,
			ToolID:     b.ToolID,
			Output:     b.Output,
		})
	}
	reactions := make([]HistoryReaction, 0, len(m.Reactions))
	for _, r := range m.Reactions {
		reactions = append(reactions, HistoryReaction{Emoji: r.Emoji, Source: r.Source})
	}
	mediaRefs := make([]HistoryMediaRef, 0, len(m.Media))
	for _, md := range m.Media {
		mediaRefs = append(mediaRefs, HistoryMediaRef{
			UploadID:  md.UploadID,
			FileName:  md.FileName,
			MimeType:  md.MimeType,
			MediaType: md.MediaType,
			URL:       md.URL,
		})
	}
	return HistoryMessage{
		ID:         string(m.ID),
		ConvID:     convID,
		Seq:        m.Seq,
		Role:       m.Role,
		Text:       m.Content,
		Source:     m.Channel,
		Model:      m.Model,
		Tier:       m.Tier,
		CostUSD:    m.CostUSD,
		DurationMs: m.DurationMs,
		SessionID:  m.SessionID,
		ReplyTo:    string(m.ReplyTo),
		CreatedAt:  time.UnixMilli(m.CreatedAt),
		Blocks:     blocks,
		Reactions:  reactions,
		Media:      mediaRefs,
	}
}

// NewMessageID generates a unique message ID.
func NewMessageID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// MediaRef references an uploaded media file attached to a chat message.
type MediaRef struct {
	UploadID string `json:"upload_id"`
	Type     string `json:"type"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	URL      string `json:"url,omitempty"`
}

// Reaction represents an emoji reaction on a message.
type Reaction struct {
	Emoji string `json:"emoji"`
	From  string `json:"from"`
}

// ChatMessage is the legacy type kept for SSE/job compatibility.
// New code should use chatdb.Message for persistence.
type ChatMessage struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	Text      string     `json:"text"`
	Model     string     `json:"model,omitempty"`
	Tier      string     `json:"tier,omitempty"`
	CostUSD   float64    `json:"cost_usd,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	ConvID    string     `json:"conv_id,omitempty"`
	ReplyTo   string     `json:"reply_to,omitempty"`
	Media     []MediaRef `json:"media,omitempty"`
	Reactions []Reaction `json:"reactions,omitempty"`
}

// ConversationInfo is the legacy type kept for API compatibility.
type ConversationInfo struct {
	ConvID      string `json:"conv_id"`
	Title       string `json:"title"`
	MsgCount    int    `json:"msg_count"`
}
