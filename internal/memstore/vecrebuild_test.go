package memstore

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCheckDimsFirstTime(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 384, ready: true}
	s, err := New(dbPath, emb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.CheckDims()

	// Should record dims without error.
	if got := s.storedDims(); got != 384 {
		t.Fatalf("expected stored dims=384, got %d", got)
	}
}

func TestCheckDimsMatch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 384, ready: true}
	s, err := New(dbPath, emb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.migrateMetaTable()
	s.setStoredDims(384)

	// Should be a no-op.
	s.CheckDims()
	if got := s.RebuildProgress(); got != -1 {
		t.Fatalf("expected no rebuild, got progress=%d", got)
	}
}

func TestCheckDimsMismatchTriggersRebuild(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 384, ready: true}
	// Disable cosine dedup so mock embedder doesn't flag as duplicates.
	s, err := New(dbPath, emb, DedupConfig{TextThreshold: 0.7, CosineThreshold: 0.001})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Store sufficiently different memories.
	s.Store("the Go programming language was created at Google in 2009", "fact", "test", nil)
	s.Store("Alessandro lives in Brussels and works on infrastructure automation", "preference", "test", nil)

	// Record old dims.
	s.migrateMetaTable()
	s.setStoredDims(384)

	// Switch to embedder with different dims.
	newEmb := &mockEmbedder{dims: 1536, ready: true}
	s.mu.Lock()
	s.embedder = newEmb
	s.mu.Unlock()

	// Trigger check — should start background rebuild.
	s.CheckDims()

	// Wait for rebuild to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.storedDims() == 1536 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := s.storedDims(); got != 1536 {
		t.Fatalf("expected dims=1536 after rebuild, got %d", got)
	}

	// Rebuild progress should be cleared.
	if got := s.RebuildProgress(); got != -1 {
		t.Fatalf("expected rebuild progress cleared, got %d", got)
	}

	// Memories should still be intact.
	if got := s.Count(); got != 2 {
		t.Fatalf("expected 2 memories after rebuild, got %d", got)
	}
}

func TestCheckDimsNoEmbedder(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	s, err := New(dbPath, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Should be safe no-op.
	s.CheckDims()
}

func TestCheckDimsEmbedderNotReady(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 384, ready: false}
	s, err := New(dbPath, emb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Should be safe no-op.
	s.CheckDims()
}

func TestRebuildEmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 1536, ready: true}
	s, err := New(dbPath, emb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.migrateMetaTable()
	s.setStoredDims(384) // old dims

	s.CheckDims()

	// Wait for background rebuild.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.storedDims() == 1536 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := s.storedDims(); got != 1536 {
		t.Fatalf("expected dims=1536 after rebuild of empty DB, got %d", got)
	}
}

func TestSetEmbedderTriggersDimsCheck(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 384, ready: true}
	s, err := New(dbPath, emb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// First CheckDims to record initial dims.
	s.CheckDims()
	if got := s.storedDims(); got != 384 {
		t.Fatalf("expected initial dims=384, got %d", got)
	}

	// SetEmbedder with same dims — should not trigger rebuild.
	newEmb := &mockEmbedder{dims: 384, ready: true}
	s.SetEmbedder(newEmb)

	// Give a moment for any potential background goroutine.
	time.Sleep(100 * time.Millisecond)
	if got := s.RebuildProgress(); got != -1 {
		t.Fatalf("expected no rebuild for same dims, got progress=%d", got)
	}
}
