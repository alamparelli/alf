package controlcenter

import (
	"crypto/rand"
	"encoding/hex"
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
	chatID    int64
	expiresAt time.Time
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
func (ms *MagicStore) Issue(chatID int64) (string, error) {
	code, err := randomHex(magicCodeLen)
	if err != nil {
		return "", err
	}

	ms.mu.Lock()
	ms.entries[code] = &magicEntry{
		chatID:    chatID,
		expiresAt: ms.nowFn().Add(magicCodeTTL),
	}
	ms.mu.Unlock()

	return code, nil
}

// Consume atomically retrieves and deletes a magic code.
// Returns the associated chat ID and true if the code was valid and not expired.
func (ms *MagicStore) Consume(code string) (int64, bool) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	entry, ok := ms.entries[code]
	if !ok {
		return 0, false
	}
	delete(ms.entries, code)

	if ms.nowFn().After(entry.expiresAt) {
		return 0, false
	}

	return entry.chatID, true
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

// SessionStore manages authenticated sessions.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
	nowFn    func() time.Time
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

// Issue creates a new session for the given chat ID and returns the session ID.
func (ss *SessionStore) Issue(chatID int64) (string, error) {
	id, err := randomHex(sessionIDLen)
	if err != nil {
		return "", err
	}

	ss.mu.Lock()
	ss.sessions[id] = &session{
		chatID:    chatID,
		expiresAt: ss.nowFn().Add(sessionTTL),
	}
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
			for id, s := range ss.sessions {
				if now.After(s.expiresAt) {
					delete(ss.sessions, id)
				}
			}
			ss.mu.Unlock()
		}
	}()
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
