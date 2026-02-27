package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry tracks one Claude session per chat.
type Entry struct {
	SessionID    string    `json:"session_id"`
	ChatID       int64     `json:"chat_id"`
	CreatedAt    time.Time `json:"created_at"`
	LastActive   time.Time `json:"last_active"`
	LastTier     string    `json:"last_tier,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
}

// Store manages per-chat Claude session IDs with timeout-based expiry.
// Session IDs come from the Claude CLI — this store only persists them.
type Store struct {
	dir     string
	timeout time.Duration
	mu      sync.Mutex
	entries map[int64]*Entry
}

// New creates a Store, loading any persisted sessions from disk.
func New(dataDir string, timeout time.Duration) *Store {
	dir := filepath.Join(dataDir, "sessions")
	s := &Store{
		dir:     dir,
		timeout: timeout,
		entries: make(map[int64]*Entry),
	}
	_ = os.MkdirAll(dir, 0o755)
	s.load()
	return s
}

// Get returns the active session ID for a chat, or "" if none/expired.
func (s *Store) Get(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[chatID]
	if !ok {
		return ""
	}
	if time.Since(e.LastActive) >= s.timeout {
		delete(s.entries, chatID)
		s.persist()
		return ""
	}
	return e.SessionID
}

// Set stores a session ID returned by Claude CLI for this chat.
func (s *Store) Set(chatID int64, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.entries[chatID] = &Entry{
		SessionID:  sessionID,
		ChatID:     chatID,
		CreatedAt:  now,
		LastActive: now,
	}
	s.persist()
}

// Touch updates the last active time for a chat's session.
func (s *Store) Touch(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[chatID]; ok {
		e.LastActive = time.Now()
		s.persist()
	}
}

// SetWithContext stores the session ID and updates routing context (last tier, message count).
func (s *Store) SetWithContext(chatID int64, sessionID, tierName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	e, ok := s.entries[chatID]
	if !ok {
		e = &Entry{
			ChatID:    chatID,
			CreatedAt: now,
		}
		s.entries[chatID] = e
	}
	e.SessionID = sessionID
	e.LastActive = now
	e.LastTier = tierName
	e.MessageCount++
	s.persist()
}

// Context returns the routing context for a chat (last tier, message count).
func (s *Store) Context(chatID int64) (lastTier string, msgCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[chatID]
	if !ok {
		return "", 0
	}
	if time.Since(e.LastActive) >= s.timeout {
		return "", 0
	}
	return e.LastTier, e.MessageCount
}

// Archive clears the session for a chat and returns the old session ID.
func (s *Store) Archive(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[chatID]
	if !ok {
		return ""
	}
	old := e.SessionID
	delete(s.entries, chatID)
	s.persist()
	return old
}

// SetTimeout updates the inactivity timeout. Safe for concurrent use.
func (s *Store) SetTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timeout = d
}

func (s *Store) mapPath() string {
	return filepath.Join(s.dir, "session_map.json")
}

func (s *Store) load() {
	data, err := os.ReadFile(s.mapPath())
	if err != nil {
		return
	}
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, e := range entries {
		s.entries[e.ChatID] = e
	}
}

// persist writes the current entries to disk atomically.
func (s *Store) persist() {
	entries := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	tmp := s.mapPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	os.Rename(tmp, s.mapPath())
}
