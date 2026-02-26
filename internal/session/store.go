package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry tracks one Claude session per chat.
type Entry struct {
	SessionID  string    `json:"session_id"`
	ChatID     int64     `json:"chat_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastActive time.Time `json:"last_active"`
}

// Store manages per-chat Claude session IDs with timeout-based expiry.
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

// GetOrCreate returns a session ID for the chat. If the session is expired
// or missing, a new one is created. Returns (sessionID, isNew).
func (s *Store) GetOrCreate(chatID int64) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[chatID]; ok {
		if time.Since(e.LastActive) < s.timeout {
			return e.SessionID, false
		}
	}

	id := newSessionID()
	now := time.Now()
	s.entries[chatID] = &Entry{
		SessionID:  id,
		ChatID:     chatID,
		CreatedAt:  now,
		LastActive: now,
	}
	s.persist()
	return id, true
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

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1e12)
	}
	// Set UUID v4 variant bits.
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
