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
	magicCodeTTL  = 24 * time.Hour
	sessionTTL    = 30 * 24 * time.Hour // 30 days
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

// Peek checks if a magic code exists and is not expired, without consuming it.
// Used by GET /auth to validate before showing the login form.
func (ms *MagicStore) Peek(code string) bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	entry, ok := ms.entries[code]
	if !ok {
		return false
	}
	return !ms.nowFn().After(entry.expiresAt)
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
	ttl       time.Duration // original TTL for sliding expiry
}

// persistedSession is the JSON-serializable form of session.
type persistedSession struct {
	ChatID    int64         `json:"chat_id"`
	ExpiresAt time.Time     `json:"expires_at"`
	TTL       time.Duration `json:"ttl,omitempty"`
}

const defaultMaxSessions = 2

// SessionStore manages authenticated sessions.
// When path is set, sessions are persisted to disk and survive restarts.
type SessionStore struct {
	mu          sync.Mutex
	sessions    map[string]*session
	nowFn       func() time.Time
	path        string // empty = in-memory only
	maxSessions int    // max concurrent sessions per chatID, 0 = defaultMaxSessions
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

// SetMaxSessions sets the maximum concurrent sessions per chatID.
func (ss *SessionStore) SetMaxSessions(n int) {
	ss.mu.Lock()
	ss.maxSessions = n
	ss.mu.Unlock()
}

// Issue creates a new session for the given chat ID with the specified TTL.
// If ttl is 0, the default sessionTTL (24h) is used.
// When the session limit is reached, the oldest session for that chatID is evicted.
func (ss *SessionStore) Issue(chatID int64, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = sessionTTL
	}

	id, err := randomHex(sessionIDLen)
	if err != nil {
		return "", err
	}

	ss.mu.Lock()
	// Enforce max sessions per chatID: evict oldest if at limit.
	max := ss.maxSessions
	if max <= 0 {
		max = defaultMaxSessions
	}
	ss.evictOldestLocked(chatID, max-1) // make room for the new one

	ss.sessions[id] = &session{
		chatID:    chatID,
		expiresAt: ss.nowFn().Add(ttl),
		ttl:       ttl,
	}
	ss.saveLocked()
	ss.mu.Unlock()

	return id, nil
}

// evictOldestLocked removes the oldest sessions for chatID until at most maxKeep remain.
// Caller MUST hold mu.
func (ss *SessionStore) evictOldestLocked(chatID int64, maxKeep int) {
	// Collect sessions for this chatID.
	type entry struct {
		id        string
		expiresAt time.Time
	}
	var entries []entry
	now := ss.nowFn()
	for id, s := range ss.sessions {
		if s.chatID == chatID {
			if now.After(s.expiresAt) {
				// Prune expired while we're here.
				delete(ss.sessions, id)
				continue
			}
			entries = append(entries, entry{id, s.expiresAt})
		}
	}

	// Evict oldest (earliest expiry) until within limit.
	for len(entries) > maxKeep {
		oldestIdx := 0
		for i := 1; i < len(entries); i++ {
			if entries[i].expiresAt.Before(entries[oldestIdx].expiresAt) {
				oldestIdx = i
			}
		}
		delete(ss.sessions, entries[oldestIdx].id)
		entries = append(entries[:oldestIdx], entries[oldestIdx+1:]...)
	}
}

// RevokeChat removes all sessions for the given chat ID.
func (ss *SessionStore) RevokeChat(chatID int64) {
	ss.mu.Lock()
	changed := false
	for id, s := range ss.sessions {
		if s.chatID == chatID {
			delete(ss.sessions, id)
			changed = true
		}
	}
	if changed {
		ss.saveLocked()
	}
	ss.mu.Unlock()
}

// Valid returns true if the session ID exists and has not expired.
// Read-only: does NOT trigger sliding expiry. Use Check when you also need to
// renew the cookie on the response.
func (ss *SessionStore) Valid(id string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	s, ok := ss.sessions[id]
	if !ok {
		return false
	}
	return !ss.nowFn().After(s.expiresAt)
}

// Check validates the session and applies sliding expiry: when past the halfway
// point of its TTL, expiresAt is extended by the original TTL from now and
// renewed is reported so the caller can re-emit the session cookie with a fresh
// MaxAge. Only the auth path (authMiddleware) should call Check — other
// call-sites that merely need validity (rate-limiter bypass, dedup) should use
// Valid so they don't consume the renewal event.
func (ss *SessionStore) Check(id string) (valid bool, renewed bool, ttl time.Duration) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	s, ok := ss.sessions[id]
	if !ok {
		return false, false, 0
	}
	now := ss.nowFn()
	if now.After(s.expiresAt) {
		return false, false, 0
	}

	// Sliding expiry: renew when past halfway point.
	if s.ttl > 0 {
		remaining := s.expiresAt.Sub(now)
		if remaining < s.ttl/2 {
			s.expiresAt = now.Add(s.ttl)
			ss.saveLocked()
			return true, true, s.ttl
		}
	}

	return true, false, 0
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
			ttl:       ps.TTL,
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
			TTL:       s.ttl,
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
