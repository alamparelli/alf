package controlcenter

import (
	"sync"
	"testing"
	"time"
)

func TestMagicStore_IssueAndConsume(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := NewMagicStore(func() time.Time { return now })

	ttl := 7 * 24 * time.Hour
	code, err := ms.Issue(12345, ttl)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(code) != 64 { // 32 bytes = 64 hex chars
		t.Fatalf("expected 64 char code, got %d", len(code))
	}

	chatID, gotTTL, ok := ms.Consume(code)
	if !ok {
		t.Fatal("Consume should succeed")
	}
	if chatID != 12345 {
		t.Errorf("expected chatID 12345, got %d", chatID)
	}
	if gotTTL != ttl {
		t.Errorf("expected TTL %v, got %v", ttl, gotTTL)
	}
}

func TestMagicStore_ConsumeDeletesCode(t *testing.T) {
	ms := NewMagicStore(nil)

	code, _ := ms.Issue(100, 24*time.Hour)
	ms.Consume(code)

	// Second consume must fail.
	_, _, ok := ms.Consume(code)
	if ok {
		t.Error("double consume should fail")
	}
}

func TestMagicStore_ExpiredCode(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := NewMagicStore(func() time.Time { return now })

	code, _ := ms.Issue(100, 24*time.Hour)

	// Advance past TTL.
	now = now.Add(magicCodeTTL + time.Second)
	ms.nowFn = func() time.Time { return now }

	_, _, ok := ms.Consume(code)
	if ok {
		t.Error("expired code should not be consumable")
	}
}

func TestMagicStore_UnknownCode(t *testing.T) {
	ms := NewMagicStore(nil)

	_, _, ok := ms.Consume("nonexistent")
	if ok {
		t.Error("unknown code should return false")
	}
}

func TestMagicStore_ConcurrentConsume(t *testing.T) {
	ms := NewMagicStore(nil)
	code, _ := ms.Issue(42, 24*time.Hour)

	var wg sync.WaitGroup
	successes := make(chan int64, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if chatID, _, ok := ms.Consume(code); ok {
				successes <- chatID
			}
		}()
	}
	wg.Wait()
	close(successes)

	count := 0
	for range successes {
		count++
	}
	if count != 1 {
		t.Errorf("expected exactly 1 successful consume, got %d", count)
	}
}

func TestSessionStore_IssueAndValid(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ss := NewSessionStore(func() time.Time { return now })

	id, err := ss.Issue(100, 24*time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !ss.Valid(id) {
		t.Error("session should be valid")
	}
}

func TestSessionStore_Expired(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ss := NewSessionStore(func() time.Time { return now })

	id, _ := ss.Issue(100, sessionTTL)

	// Advance past TTL.
	now = now.Add(sessionTTL + time.Second)
	ss.nowFn = func() time.Time { return now }

	if ss.Valid(id) {
		t.Error("expired session should not be valid")
	}
}

func TestSessionStore_CustomTTL(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ss := NewSessionStore(func() time.Time { return now })

	ttl := 7 * 24 * time.Hour
	id, _ := ss.Issue(100, ttl)

	// Advance past default 24h but within 7 days — should still be valid.
	now = now.Add(48 * time.Hour)
	ss.nowFn = func() time.Time { return now }

	if !ss.Valid(id) {
		t.Error("session with 7d TTL should be valid after 48h")
	}

	// Advance past 7 days — should be expired.
	now = now.Add(6 * 24 * time.Hour)
	ss.nowFn = func() time.Time { return now }

	if ss.Valid(id) {
		t.Error("session with 7d TTL should be expired after 8 days")
	}
}

func TestSessionStore_ZeroTTLUsesDefault(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ss := NewSessionStore(func() time.Time { return now })

	id, _ := ss.Issue(100, 0)

	// Should use default 24h TTL.
	now = now.Add(sessionTTL + time.Second)
	ss.nowFn = func() time.Time { return now }

	if ss.Valid(id) {
		t.Error("session with zero TTL should default to 24h and be expired")
	}
}

func TestSessionStore_UnknownID(t *testing.T) {
	ss := NewSessionStore(nil)

	if ss.Valid("nonexistent") {
		t.Error("unknown session should be invalid")
	}
}

func TestSessionStore_MaxSessionsEvictsOldest(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ss := NewSessionStore(func() time.Time { return now })
	ss.SetMaxSessions(2)

	// Issue 2 sessions.
	id1, _ := ss.Issue(100, 24*time.Hour)
	now = now.Add(time.Minute)
	id2, _ := ss.Issue(100, 24*time.Hour)

	if !ss.Valid(id1) || !ss.Valid(id2) {
		t.Fatal("both sessions should be valid")
	}

	// Issue a 3rd — oldest (id1) should be evicted.
	now = now.Add(time.Minute)
	id3, _ := ss.Issue(100, 24*time.Hour)

	if ss.Valid(id1) {
		t.Error("oldest session should have been evicted")
	}
	if !ss.Valid(id2) {
		t.Error("second session should still be valid")
	}
	if !ss.Valid(id3) {
		t.Error("new session should be valid")
	}
}

func TestSessionStore_MaxSessionsPerChatID(t *testing.T) {
	ss := NewSessionStore(nil)
	ss.SetMaxSessions(2)

	// Sessions for different chatIDs don't interfere.
	a1, _ := ss.Issue(100, 24*time.Hour)
	a2, _ := ss.Issue(100, 24*time.Hour)
	b1, _ := ss.Issue(200, 24*time.Hour)

	// Adding a 3rd for chatID=100 evicts one, but chatID=200 is untouched.
	a3, _ := ss.Issue(100, 24*time.Hour)

	if ss.Valid(a1) {
		t.Error("oldest session for chatID=100 should be evicted")
	}
	if !ss.Valid(a2) || !ss.Valid(a3) {
		t.Error("remaining sessions for chatID=100 should be valid")
	}
	if !ss.Valid(b1) {
		t.Error("session for chatID=200 should be unaffected")
	}
}

func TestFileSessionStore_PersistAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sessions.json"
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create store, issue a session.
	ss1 := NewFileSessionStore(path, func() time.Time { return now })
	id, err := ss1.Issue(12345, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !ss1.Valid(id) {
		t.Fatal("session should be valid in first store")
	}

	// Simulate restart: create a new store from same file.
	ss2 := NewFileSessionStore(path, func() time.Time { return now })
	if !ss2.Valid(id) {
		t.Error("session should survive restart and be valid in second store")
	}
}

func TestFileSessionStore_PrunesExpiredOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sessions.json"
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Issue with short TTL.
	ss1 := NewFileSessionStore(path, func() time.Time { return t1 })
	id, _ := ss1.Issue(100, 1*time.Hour)

	// Reload after expiration.
	t2 := t1.Add(2 * time.Hour)
	ss2 := NewFileSessionStore(path, func() time.Time { return t2 })
	if ss2.Valid(id) {
		t.Error("expired session should be pruned on load")
	}
}
