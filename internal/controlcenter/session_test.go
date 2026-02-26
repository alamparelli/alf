package controlcenter

import (
	"sync"
	"testing"
	"time"
)

func TestMagicStore_IssueAndConsume(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := NewMagicStore(func() time.Time { return now })

	code, err := ms.Issue(12345)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(code) != 64 { // 32 bytes = 64 hex chars
		t.Fatalf("expected 64 char code, got %d", len(code))
	}

	chatID, ok := ms.Consume(code)
	if !ok {
		t.Fatal("Consume should succeed")
	}
	if chatID != 12345 {
		t.Errorf("expected chatID 12345, got %d", chatID)
	}
}

func TestMagicStore_ConsumeDeletesCode(t *testing.T) {
	ms := NewMagicStore(nil)

	code, _ := ms.Issue(100)
	ms.Consume(code)

	// Second consume must fail.
	_, ok := ms.Consume(code)
	if ok {
		t.Error("double consume should fail")
	}
}

func TestMagicStore_ExpiredCode(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := NewMagicStore(func() time.Time { return now })

	code, _ := ms.Issue(100)

	// Advance past TTL.
	now = now.Add(magicCodeTTL + time.Second)
	ms.nowFn = func() time.Time { return now }

	_, ok := ms.Consume(code)
	if ok {
		t.Error("expired code should not be consumable")
	}
}

func TestMagicStore_UnknownCode(t *testing.T) {
	ms := NewMagicStore(nil)

	_, ok := ms.Consume("nonexistent")
	if ok {
		t.Error("unknown code should return false")
	}
}

func TestMagicStore_ConcurrentConsume(t *testing.T) {
	ms := NewMagicStore(nil)
	code, _ := ms.Issue(42)

	var wg sync.WaitGroup
	successes := make(chan int64, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if chatID, ok := ms.Consume(code); ok {
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

	id, err := ss.Issue(100)
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

	id, _ := ss.Issue(100)

	// Advance past TTL.
	now = now.Add(sessionTTL + time.Second)
	ss.nowFn = func() time.Time { return now }

	if ss.Valid(id) {
		t.Error("expired session should not be valid")
	}
}

func TestSessionStore_UnknownID(t *testing.T) {
	ss := NewSessionStore(nil)

	if ss.Valid("nonexistent") {
		t.Error("unknown session should be invalid")
	}
}
