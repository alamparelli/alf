package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGetOrCreate_NewSession(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	id, isNew := s.GetOrCreate(123)
	if !isNew {
		t.Error("expected isNew=true for first call")
	}
	if id == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestGetOrCreate_ExistingSession(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	id1, _ := s.GetOrCreate(123)
	id2, isNew := s.GetOrCreate(123)

	if isNew {
		t.Error("expected isNew=false for second call")
	}
	if id1 != id2 {
		t.Errorf("session IDs should match: %q != %q", id1, id2)
	}
}

func TestGetOrCreate_TimeoutExpiry(t *testing.T) {
	s := New(t.TempDir(), 1*time.Millisecond)

	id1, _ := s.GetOrCreate(123)
	time.Sleep(5 * time.Millisecond)
	id2, isNew := s.GetOrCreate(123)

	if !isNew {
		t.Error("expected isNew=true after timeout")
	}
	if id1 == id2 {
		t.Error("expired session should get a new ID")
	}
}

func TestTouch_ExtendsSession(t *testing.T) {
	s := New(t.TempDir(), 50*time.Millisecond)

	s.GetOrCreate(123)
	time.Sleep(30 * time.Millisecond)
	s.Touch(123)
	time.Sleep(30 * time.Millisecond)

	_, isNew := s.GetOrCreate(123)
	if isNew {
		t.Error("touch should have extended the session")
	}
}

func TestArchive(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	id1, _ := s.GetOrCreate(123)
	old := s.Archive(123)
	if old != id1 {
		t.Errorf("Archive should return old ID: %q != %q", old, id1)
	}

	id2, isNew := s.GetOrCreate(123)
	if !isNew {
		t.Error("expected isNew=true after archive")
	}
	if id1 == id2 {
		t.Error("archived session should get a new ID")
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

	s.GetOrCreate(123)
	s.SetTimeout(1 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	_, isNew := s.GetOrCreate(123)
	if !isNew {
		t.Error("expected new session after timeout changed to 1ms")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()

	s1 := New(dir, 30*time.Minute)
	id1, _ := s1.GetOrCreate(42)

	// Create a second store from same dir — should load persisted data.
	s2 := New(dir, 30*time.Minute)
	id2, isNew := s2.GetOrCreate(42)

	if isNew {
		t.Error("expected session to be loaded from disk")
	}
	if id1 != id2 {
		t.Errorf("persisted session ID mismatch: %q != %q", id1, id2)
	}
}

func TestDifferentChats(t *testing.T) {
	s := New(t.TempDir(), 30*time.Minute)

	id1, _ := s.GetOrCreate(1)
	id2, _ := s.GetOrCreate(2)

	if id1 == id2 {
		t.Error("different chats should get different session IDs")
	}
}

func TestSessionDir_Created(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s := New(dir, 30*time.Minute)
	s.GetOrCreate(1)
	// If we got here without panic, the dir was created.
}
