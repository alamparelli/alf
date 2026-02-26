package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGet_Empty(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)
	if id := s.Get(123); id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

func TestSet_ThenGet(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	s.Set(123, "uuid-from-claude")
	id := s.Get(123)
	if id != "uuid-from-claude" {
		t.Errorf("expected 'uuid-from-claude', got %q", id)
	}
}

func TestGet_TimeoutExpiry(t *testing.T) {
	s := New(t.TempDir(), 1*time.Millisecond)

	s.Set(123, "uuid-1")
	time.Sleep(5 * time.Millisecond)

	if id := s.Get(123); id != "" {
		t.Errorf("expected empty after timeout, got %q", id)
	}
}

func TestTouch_ExtendsSession(t *testing.T) {
	s := New(t.TempDir(), 50*time.Millisecond)

	s.Set(123, "uuid-1")
	time.Sleep(30 * time.Millisecond)
	s.Touch(123)
	time.Sleep(30 * time.Millisecond)

	if id := s.Get(123); id != "uuid-1" {
		t.Errorf("touch should have extended session, got %q", id)
	}
}

func TestArchive(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	s.Set(123, "uuid-1")
	old := s.Archive(123)
	if old != "uuid-1" {
		t.Errorf("Archive should return old ID: got %q", old)
	}
	if id := s.Get(123); id != "" {
		t.Errorf("expected empty after archive, got %q", id)
	}
}

func TestArchive_Empty(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)
	if old := s.Archive(999); old != "" {
		t.Errorf("Archive on missing chat should return empty, got %q", old)
	}
}

func TestSetTimeout(t *testing.T) {
	s := New(t.TempDir(), 1*time.Hour)

	s.Set(123, "uuid-1")
	s.SetTimeout(1 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	if id := s.Get(123); id != "" {
		t.Errorf("expected empty after timeout change, got %q", id)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()

	s1 := New(dir, 30*time.Minute)
	s1.Set(42, "uuid-persisted")

	// Second store from same dir should load persisted data.
	s2 := New(dir, 30*time.Minute)
	if id := s2.Get(42); id != "uuid-persisted" {
		t.Errorf("expected persisted ID, got %q", id)
	}
}

func TestDifferentChats(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	s.Set(1, "uuid-a")
	s.Set(2, "uuid-b")

	if s.Get(1) != "uuid-a" || s.Get(2) != "uuid-b" {
		t.Error("different chats should have independent sessions")
	}
}

func TestSet_Overwrites(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	s.Set(1, "old-uuid")
	s.Set(1, "new-uuid")

	if id := s.Get(1); id != "new-uuid" {
		t.Errorf("expected overwritten ID 'new-uuid', got %q", id)
	}
}

func TestSessionDir_Created(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s := New(dir, 30*time.Minute)
	s.Set(1, "x")
	// If we got here without panic, the dir was created.
}
