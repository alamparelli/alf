package controlcenter

import (
	"bufio"
	"encoding/json"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ChatMessage represents a single chat message (user or assistant).
type ChatMessage struct {
	ID        string      `json:"id"`
	Role      string      `json:"role"` // "user" | "assistant"
	Text      string      `json:"text"`
	Timestamp time.Time   `json:"ts"`
	Model     string      `json:"model,omitempty"`
	Tier      string      `json:"tier,omitempty"`
	CostUSD   float64     `json:"cost_usd,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	ReplyTo   string      `json:"reply_to,omitempty"`
	Media     []MediaRef  `json:"media,omitempty"`
	Reactions []Reaction  `json:"reactions,omitempty"`
}

// MediaRef references an uploaded media file.
type MediaRef struct {
	UploadID string `json:"upload_id"`
	Type     string `json:"type"`     // photo, document, video, voice
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	URL      string `json:"url,omitempty"` // /api/chat/media/<upload_id>
}

// Reaction represents an emoji reaction on a message.
type Reaction struct {
	Emoji string `json:"emoji"`
	From  string `json:"from"` // "user" | "alf"
}

// ChatStore persists chat messages as JSONL and keeps a ring buffer in memory.
type ChatStore struct {
	mu       sync.RWMutex
	ring     []ChatMessage
	ringPos  int
	ringFull bool
	filePath string
}

const chatRingSize = 200

// NewChatStore creates a new chat store backed by a JSONL file.
func NewChatStore(dataDir string) *ChatStore {
	dir := filepath.Join(dataDir, "logs")
	os.MkdirAll(dir, 0o755)
	cs := &ChatStore{
		ring:     make([]ChatMessage, chatRingSize),
		filePath: filepath.Join(dir, "chat_messages.jsonl"),
	}
	cs.loadExisting()
	return cs
}

// NewMessageID generates a unique message ID.
func NewMessageID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Append adds a message to the store (memory ring + disk JSONL).
func (cs *ChatStore) Append(msg ChatMessage) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.ring[cs.ringPos] = msg
	cs.ringPos = (cs.ringPos + 1) % len(cs.ring)
	if cs.ringPos == 0 {
		cs.ringFull = true
	}
	cs.appendDisk(msg)
}

// Get returns a message by ID from the ring buffer. Returns nil if not found.
func (cs *ChatStore) Get(id string) *ChatMessage {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for _, msg := range cs.ringMessages() {
		if msg.ID == id {
			cp := msg
			return &cp
		}
	}
	return nil
}

// AddReaction appends a reaction to a message in the ring buffer and re-persists.
func (cs *ChatStore) AddReaction(msgID string, r Reaction) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	limit := len(cs.ring)
	if !cs.ringFull {
		limit = cs.ringPos
	}
	for i := 0; i < limit; i++ {
		if cs.ring[i].ID == msgID {
			cs.ring[i].Reactions = append(cs.ring[i].Reactions, r)
			return true
		}
	}
	return false
}

// Recent returns the last n messages in chronological order.
func (cs *ChatStore) Recent(n int) []ChatMessage {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	msgs := cs.ringMessages()
	if n > 0 && n < len(msgs) {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs
}

// History returns messages before a given timestamp, limited to n messages.
func (cs *ChatStore) History(limit int, before time.Time) []ChatMessage {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	all := cs.ringMessages()
	var result []ChatMessage
	for i := len(all) - 1; i >= 0 && len(result) < limit; i-- {
		if before.IsZero() || all[i].Timestamp.Before(before) {
			result = append(result, all[i])
		}
	}
	// Reverse to chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// ringMessages returns all messages in chronological order (unlocked).
func (cs *ChatStore) ringMessages() []ChatMessage {
	if !cs.ringFull {
		result := make([]ChatMessage, cs.ringPos)
		copy(result, cs.ring[:cs.ringPos])
		return result
	}
	result := make([]ChatMessage, len(cs.ring))
	copy(result, cs.ring[cs.ringPos:])
	copy(result[len(cs.ring)-cs.ringPos:], cs.ring[:cs.ringPos])
	return result
}

func (cs *ChatStore) appendDisk(msg ChatMessage) {
	f, err := os.OpenFile(cs.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	f.Write(data)
	f.Write([]byte("\n"))
}

func (cs *ChatStore) loadExisting() {
	f, err := os.Open(cs.filePath)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	for scanner.Scan() {
		var msg ChatMessage
		if json.Unmarshal(scanner.Bytes(), &msg) == nil && msg.ID != "" {
			cs.ring[cs.ringPos] = msg
			cs.ringPos = (cs.ringPos + 1) % len(cs.ring)
			if cs.ringPos == 0 {
				cs.ringFull = true
			}
		}
	}
}
