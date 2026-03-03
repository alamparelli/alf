package controlcenter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	magicCodeTTL  = 5 * time.Minute
	sessionTTL    = 24 * time.Hour
	magicCodeLen  = 32 // bytes → 64 hex chars
	sessionIDLen  = 32
)

// magicEntry is a pending magic link code.
type magicEntry struct {
	chatID     int64
	sessionTTL time.Duration
	expiresAt  time.Time
}

// MagicStore manages short-lived magic link codes for authentication.
type MagicStore struct {
	mu      sync.Mutex
	entries map[string]*magicEntry
	nowFn   func() time.Time
}

// NewMagicStore creates a MagicStore. Pass nil for nowFn to use time.Now.
func NewMagicStore(nowFn func() time.Time) *MagicStore {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &MagicStore{
		entries: make(map[string]*magicEntry),
		nowFn:   nowFn,
	}
}

// Issue creates a new magic code for the given chat ID.
// The sessionTTL is carried through and returned on Consume so the auth handler
// can create a session with the correct duration.
func (ms *MagicStore) Issue(chatID int64, sessTTL time.Duration) (string, error) {
	code, err := randomHex(magicCodeLen)
	if err != nil {
		return "", err
	}

	ms.mu.Lock()
	// Invalidate any existing codes for this chat ID (one active link at a time).
	for k, e := range ms.entries {
		if e.chatID == chatID {
			delete(ms.entries, k)
		}
	}
	ms.entries[code] = &magicEntry{
		chatID:     chatID,
		sessionTTL: sessTTL,
		expiresAt:  ms.nowFn().Add(magicCodeTTL),
	}
	ms.mu.Unlock()

	return code, nil
}

// Consume atomically retrieves and deletes a magic code.
// Returns the associated chat ID, the requested session TTL, and true if the code was valid and not expired.
func (ms *MagicStore) Consume(code string) (int64, time.Duration, bool) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	entry, ok := ms.entries[code]
	if !ok {
		return 0, 0, false
	}
	delete(ms.entries, code)

	if ms.nowFn().After(entry.expiresAt) {
		return 0, 0, false
	}

	return entry.chatID, entry.sessionTTL, true
}

// StartCleanup runs a background goroutine that sweeps expired entries every minute.
func (ms *MagicStore) StartCleanup() {
	go func() {
		for {
			time.Sleep(time.Minute)
			ms.mu.Lock()
			now := ms.nowFn()
			for code, entry := range ms.entries {
				if now.After(entry.expiresAt) {
					delete(ms.entries, code)
				}
			}
			ms.mu.Unlock()
		}
	}()
}

// session represents an authenticated user session.
type session struct {
	chatID    int64
	expiresAt time.Time
}

// persistedSession is the JSON-serializable form of session.
type persistedSession struct {
	ChatID    int64     `json:"chat_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionStore manages authenticated sessions.
// When path is set, sessions are persisted to disk and survive restarts.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
	nowFn    func() time.Time
	path     string // empty = in-memory only
}

// NewSessionStore creates a SessionStore. Pass nil for nowFn to use time.Now.
func NewSessionStore(nowFn func() time.Time) *SessionStore {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &SessionStore{
		sessions: make(map[string]*session),
		nowFn:    nowFn,
	}
}

// NewFileSessionStore creates a SessionStore backed by a JSON file.
// Existing sessions are loaded from disk, expired ones are pruned.
func NewFileSessionStore(path string, nowFn func() time.Time) *SessionStore {
	if nowFn == nil {
		nowFn = time.Now
	}
	ss := &SessionStore{
		sessions: make(map[string]*session),
		nowFn:    nowFn,
		path:     path,
	}
	if err := ss.load(); err != nil {
		log.Printf("warning: could not load sessions from %s: %v", path, err)
	}
	return ss
}

// Issue creates a new session for the given chat ID with the specified TTL.
// If ttl is 0, the default sessionTTL (24h) is used.
func (ss *SessionStore) Issue(chatID int64, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = sessionTTL
	}

	id, err := randomHex(sessionIDLen)
	if err != nil {
		return "", err
	}

	ss.mu.Lock()
	ss.sessions[id] = &session{
		chatID:    chatID,
		expiresAt: ss.nowFn().Add(ttl),
	}
	ss.saveLocked()
	ss.mu.Unlock()

	return id, nil
}

// Valid returns true if the session ID exists and has not expired.
func (ss *SessionStore) Valid(id string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	s, ok := ss.sessions[id]
	if !ok {
		return false
	}
	return !ss.nowFn().After(s.expiresAt)
}

// StartCleanup runs a background goroutine that sweeps expired sessions every 15 minutes.
func (ss *SessionStore) StartCleanup() {
	go func() {
		for {
			time.Sleep(15 * time.Minute)
			ss.mu.Lock()
			now := ss.nowFn()
			changed := false
			for id, s := range ss.sessions {
				if now.After(s.expiresAt) {
					delete(ss.sessions, id)
					changed = true
				}
			}
			if changed {
				ss.saveLocked()
			}
			ss.mu.Unlock()
		}
	}()
}

// load reads sessions from disk. Caller must NOT hold mu.
func (ss *SessionStore) load() error {
	if ss.path == "" {
		return nil
	}

	data, err := os.ReadFile(ss.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sessions: %w", err)
	}

	var persisted map[string]*persistedSession
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("parse sessions: %w", err)
	}

	now := ss.nowFn()
	for id, ps := range persisted {
		if now.After(ps.ExpiresAt) {
			continue // prune expired
		}
		ss.sessions[id] = &session{
			chatID:    ps.ChatID,
			expiresAt: ps.ExpiresAt,
		}
	}

	log.Printf("loaded %d active sessions from disk", len(ss.sessions))
	return nil
}

// saveLocked writes sessions to disk. Caller MUST hold mu.
func (ss *SessionStore) saveLocked() {
	if ss.path == "" {
		return
	}

	persisted := make(map[string]*persistedSession, len(ss.sessions))
	for id, s := range ss.sessions {
		persisted[id] = &persistedSession{
			ChatID:    s.chatID,
			ExpiresAt: s.expiresAt,
		}
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		log.Printf("warning: marshal sessions: %v", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(ss.path), 0o755); err != nil {
		log.Printf("warning: create sessions dir: %v", err)
		return
	}

	tmp := ss.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("warning: write sessions: %v", err)
		return
	}
	if err := os.Rename(tmp, ss.path); err != nil {
		os.Remove(tmp)
		log.Printf("warning: rename sessions: %v", err)
	}
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
