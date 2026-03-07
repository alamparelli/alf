package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Message represents a single chat message for API-based providers.
type Message struct {
	Role    string `json:"role"`    // "user", "assistant", "system"
	Content string `json:"content"`
}

// History stores per-session message history for API providers.
// It replaces --resume for non-CLI backends.
type History struct {
	mu       sync.Mutex
	sessions map[string]*historyEntry
	maxMsgs  int
	dataDir  string
	expiry   time.Duration
}

type historyEntry struct {
	Messages []Message `json:"messages"`
	LastUsed time.Time `json:"last_used"`
}

// NewHistory creates a History store.
// dataDir is the base data directory; histories are stored in dataDir/api_history/.
// maxMsgs is the sliding window size (oldest pairs dropped when exceeded).
// expiry is the inactivity timeout after which history is cleared (0 = 1 hour).
func NewHistory(dataDir string, maxMsgs int, expiry time.Duration) *History {
	if maxMsgs <= 0 {
		maxMsgs = 100
	}
	if expiry <= 0 {
		expiry = time.Hour
	}
	h := &History{
		sessions: make(map[string]*historyEntry),
		maxMsgs:  maxMsgs,
		dataDir:  filepath.Join(dataDir, "api_history"),
		expiry:   expiry,
	}
	os.MkdirAll(h.dataDir, 0o755)
	return h
}

// Append adds a message to the session history and persists to disk.
func (h *History) Append(key string, msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry := h.loadLocked(key)
	entry.Messages = append(entry.Messages, msg)
	entry.LastUsed = time.Now()

	// Sliding window: drop oldest pairs.
	if len(entry.Messages) > h.maxMsgs {
		// Drop the oldest 2 messages (user+assistant pair).
		drop := len(entry.Messages) - h.maxMsgs
		if drop%2 != 0 {
			drop++ // keep pairs aligned
		}
		if drop > 0 && drop < len(entry.Messages) {
			entry.Messages = entry.Messages[drop:]
		}
	}

	h.sessions[key] = entry
	h.persistLocked(key, entry)
}

// Get returns the message history for a session key.
// Returns nil if expired or not found.
func (h *History) Get(key string) []Message {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry := h.loadLocked(key)
	if len(entry.Messages) == 0 {
		return nil
	}

	if time.Since(entry.LastUsed) > h.expiry {
		h.clearLocked(key)
		return nil
	}

	// Return a copy.
	msgs := make([]Message, len(entry.Messages))
	copy(msgs, entry.Messages)
	return msgs
}

// Clear removes all history for a session key.
func (h *History) Clear(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clearLocked(key)
}

func (h *History) loadLocked(key string) *historyEntry {
	if entry, ok := h.sessions[key]; ok {
		return entry
	}

	// Try loading from disk.
	path := h.filePath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		entry := &historyEntry{LastUsed: time.Now()}
		h.sessions[key] = entry
		return entry
	}

	var entry historyEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		entry = historyEntry{LastUsed: time.Now()}
	}
	h.sessions[key] = &entry
	return &entry
}

func (h *History) clearLocked(key string) {
	delete(h.sessions, key)
	os.Remove(h.filePath(key))
}

func (h *History) persistLocked(key string, entry *historyEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	// Atomic write: write to temp file then rename.
	tmp := h.filePath(key) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	os.Rename(tmp, h.filePath(key))
}

func (h *History) filePath(key string) string {
	// Sanitize key for filesystem safety.
	safe := sanitizeKey(key)
	return filepath.Join(h.dataDir, safe+".json")
}

func sanitizeKey(key string) string {
	var b []byte
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b = append(b, byte(r))
		default:
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return "default"
	}
	return string(b)
}
