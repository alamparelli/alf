package conversation

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Store persists rich conversation messages as JSONL and keeps a ring buffer in memory.
// One Store instance is shared across all frontends (Telegram, CC).
// Messages are scoped by Channel+ConvID - each channel (tg, cc) has its own
// active conversation. NewConversation(channel) rotates that channel's ID.
type Store struct {
	mu       sync.RWMutex
	ring     []Message
	ringPos  int
	ringFull bool
	filePath string
	convIDs  map[string]string // channel → active conv ID
}

const ringSize = 200

// NewStore creates a conversation store backed by a JSONL file.
func NewStore(dataDir string) *Store {
	dir := filepath.Join(dataDir, "logs")
	os.MkdirAll(dir, 0o755)
	s := &Store{
		ring:     make([]Message, ringSize),
		filePath: filepath.Join(dir, "conversation.jsonl"),
		convIDs:  make(map[string]string),
	}
	s.loadExisting()
	return s
}

// ConvID returns the current active conversation ID for a channel.
// Creates one if it doesn't exist yet.
func (s *Store) ConvID(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.convIDs[channel]
	if !ok {
		id = NewConvID()
		s.convIDs[channel] = id
	}
	return id
}

// NewConversation rotates to a new conversation ID for the given channel
// and returns the old one. Subsequent Append() calls with this channel's
// ConvID will use the new ID. Recent() only returns messages from the new conversation.
func (s *Store) NewConversation(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.convIDs[channel]
	s.convIDs[channel] = NewConvID()
	log.Printf("[convstore] new conversation %s for channel %q (was %s)", s.convIDs[channel], channel, old)
	return old
}

// Append adds a message to the store (memory ring + disk JSONL).
// The caller must set msg.ConvID before calling (use ConvID(channel) to get it).
func (s *Store) Append(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring[s.ringPos] = msg
	s.ringPos = (s.ringPos + 1) % len(s.ring)
	if s.ringPos == 0 {
		s.ringFull = true
	}
	s.appendDisk(msg)
}

// Get returns a message by ID from the ring buffer. Returns nil if not found.
func (s *Store) Get(id string) *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, msg := range s.ordered() {
		if msg.ID == id {
			cp := msg
			return &cp
		}
	}
	return nil
}

// Recent returns the last n messages from the given channel's active conversation.
// Pass n=0 for all messages in the current conversation.
func (s *Store) Recent(channel string, n int) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	convID := s.convIDs[channel]
	if convID == "" {
		return nil
	}
	all := s.ordered()
	var msgs []Message
	for _, m := range all {
		if m.ConvID == convID {
			msgs = append(msgs, m)
		}
	}
	if n > 0 && n < len(msgs) {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs
}

// RecentAll returns the last n messages regardless of channel or conversation.
// Used for full history display (e.g. CC chat history endpoint).
func (s *Store) RecentAll(n int) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := s.ordered()
	if n > 0 && n < len(msgs) {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs
}

// AddReaction appends a reaction to a message in the ring buffer.
func (s *Store) AddReaction(msgID string, r Reaction) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := len(s.ring)
	if !s.ringFull {
		limit = s.ringPos
	}
	for i := 0; i < limit; i++ {
		if s.ring[i].ID == msgID {
			s.ring[i].Reactions = append(s.ring[i].Reactions, r)
			return true
		}
	}
	return false
}

// ordered returns all messages in chronological order (caller must hold lock).
func (s *Store) ordered() []Message {
	if !s.ringFull {
		result := make([]Message, s.ringPos)
		copy(result, s.ring[:s.ringPos])
		return result
	}
	result := make([]Message, len(s.ring))
	copy(result, s.ring[s.ringPos:])
	copy(result[len(s.ring)-s.ringPos:], s.ring[:s.ringPos])
	return result
}

func (s *Store) appendDisk(msg Message) {
	f, err := os.OpenFile(s.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
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

func (s *Store) loadExisting() {
	f, err := os.Open(s.filePath)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	// Track the last ConvID seen per channel to resume after restart.
	lastConvPerChannel := make(map[string]string)
	for scanner.Scan() {
		var msg Message
		if json.Unmarshal(scanner.Bytes(), &msg) == nil && msg.ID != "" {
			s.ring[s.ringPos] = msg
			s.ringPos = (s.ringPos + 1) % len(s.ring)
			if s.ringPos == 0 {
				s.ringFull = true
			}
			if msg.ConvID != "" && msg.Channel != "" {
				lastConvPerChannel[msg.Channel] = msg.ConvID
			}
		}
	}
	// Resume per-channel conversation IDs from disk.
	for ch, id := range lastConvPerChannel {
		s.convIDs[ch] = id
	}
}
