package controlcenter

import (
	"crypto/rand"
	"fmt"
)

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
