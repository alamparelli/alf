package session

import (
	"reflect"
	"testing"
	"time"
)

func TestSetWithContext_CreatesEntryAndUpdatesCounter(t *testing.T) {
	s := New(t.TempDir(), time.Hour)

	s.SetWithContext(42, "sess-1", "fast")
	tier, count := s.Context(42)
	if tier != "fast" || count != 1 {
		t.Errorf("expected (fast, 1), got (%q, %d)", tier, count)
	}
	// Session ID should also be set.
	if s.Get(42) != "sess-1" {
		t.Errorf("session ID not stored")
	}

	// Second call updates session id and increments counter.
	s.SetWithContext(42, "sess-2", "smart")
	tier, count = s.Context(42)
	if tier != "smart" || count != 2 {
		t.Errorf("expected (smart, 2), got (%q, %d)", tier, count)
	}
	if s.Get(42) != "sess-2" {
		t.Errorf("session ID not updated")
	}
}

func TestSetWithBackend_SetsBackend(t *testing.T) {
	s := New(t.TempDir(), time.Hour)

	s.SetWithBackend(1, "sid", "fast", "openrouter")
	tier, backend, count := s.ContextFull(1)
	if tier != "fast" || backend != "openrouter" || count != 1 {
		t.Errorf("expected (fast, openrouter, 1), got (%q, %q, %d)", tier, backend, count)
	}
}

func TestTouchContext_IncrementsWithoutChangingSessionID(t *testing.T) {
	s := New(t.TempDir(), time.Hour)
	s.Set(1, "existing-sid")

	s.TouchContext(1, "fast")
	if s.Get(1) != "existing-sid" {
		t.Errorf("TouchContext must not modify session id")
	}
	tier, count := s.Context(1)
	if tier != "fast" || count != 1 {
		t.Errorf("expected (fast, 1), got (%q, %d)", tier, count)
	}
}

func TestContext_EmptyForUnknownChat(t *testing.T) {
	s := New(t.TempDir(), time.Hour)
	tier, count := s.Context(999)
	if tier != "" || count != 0 {
		t.Errorf("unknown chat should yield (\"\", 0), got (%q, %d)", tier, count)
	}
	_, _, count2 := s.ContextFull(999)
	if count2 != 0 {
		t.Errorf("unknown chat ContextFull should be zero, got %d", count2)
	}
}

func TestContext_TimeoutReturnsZero(t *testing.T) {
	s := New(t.TempDir(), time.Nanosecond)
	s.SetWithContext(1, "sid", "fast")
	time.Sleep(2 * time.Millisecond)

	tier, count := s.Context(1)
	if tier != "" || count != 0 {
		t.Errorf("expired context must return zero values, got (%q, %d)", tier, count)
	}
	if _, _, c := s.ContextFull(1); c != 0 {
		t.Errorf("expired ContextFull must return zero, got %d", c)
	}
}

func TestSkills_AddGetRemoveClear(t *testing.T) {
	s := New(t.TempDir(), time.Hour)

	// Unknown chat → nil.
	if got := s.GetSkills(1); got != nil {
		t.Errorf("expected nil for unknown chat, got %v", got)
	}

	s.AddSkills(1, []string{"a", "b"})
	s.AddSkills(1, []string{"b", "c"}) // dedup 'b'

	got := s.GetSkills(1)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("expected [a b c], got %v", got)
	}

	s.RemoveSkill(1, "b")
	if got := s.GetSkills(1); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("after remove: expected [a c], got %v", got)
	}

	s.ClearSkills(1)
	if got := s.GetSkills(1); len(got) != 0 {
		t.Errorf("ClearSkills should leave no skills, got %v", got)
	}

	// RemoveSkill + ClearSkills on unknown chat must be no-ops.
	s.RemoveSkill(99, "x")
	s.ClearSkills(99)
}

func TestSkills_TimeoutReturnsNil(t *testing.T) {
	s := New(t.TempDir(), time.Nanosecond)
	s.AddSkills(1, []string{"a"})
	time.Sleep(2 * time.Millisecond)
	if got := s.GetSkills(1); got != nil {
		t.Errorf("expired GetSkills must return nil, got %v", got)
	}
}

func TestForcedTier_SetGet(t *testing.T) {
	s := New(t.TempDir(), time.Hour)

	if got := s.GetForcedTier(1); got != "" {
		t.Errorf("unknown chat should have no forced tier, got %q", got)
	}

	s.SetForcedTier(1, "smart")
	if got := s.GetForcedTier(1); got != "smart" {
		t.Errorf("expected smart, got %q", got)
	}
}

func TestForcedTier_TimeoutEvictsEntry(t *testing.T) {
	s := New(t.TempDir(), time.Nanosecond)
	s.SetForcedTier(1, "smart")
	time.Sleep(2 * time.Millisecond)
	if got := s.GetForcedTier(1); got != "" {
		t.Errorf("expired forced tier must be cleared, got %q", got)
	}
	// Entry should be evicted.
	if s.Get(1) != "" {
		t.Errorf("expired entry should be evicted")
	}
}
