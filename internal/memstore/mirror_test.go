package memstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memstore"
)

// TestStore_SetMirror_ForwardsWrites verifies that once SetMirror has been
// called, every successful memstore.Store.Store() also lands in the
// memory.Store as an Index call under the corresponding scope.
//
// Dual-write is the #337c1 transition path: writers keep using memstore
// while the unified memory.db fills up with the same facts, so readers
// (sub-ticket C2) can migrate without losing data.
func TestStore_SetMirror_ForwardsWrites(t *testing.T) {
	mirror, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	defer mirror.Close()

	dbPath := filepath.Join(t.TempDir(), "memstore.db")
	s, err := memstore.New(dbPath, nil)
	if err != nil {
		t.Fatalf("memstore.New: %v", err)
	}
	defer s.Close()

	s.SetMirror(mirror)

	id, err := s.Store("the cat sat on the mat", "fact", "extractor", nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Read back from the mirror — memory.Store with no embedder uses LIKE,
	// so the query must be a contiguous substring of the stored text.
	hits, err := mirror.Search(context.Background(), "fact", "cat sat", 5)
	if err != nil {
		t.Fatalf("mirror.Search: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.Document.Text == "the cat sat on the mat" {
			found = true
			if h.Document.Metadata["source"] != "extractor" {
				t.Errorf("mirror dropped source metadata: got %q", h.Document.Metadata["source"])
			}
			break
		}
	}
	if !found {
		t.Fatalf("mirror did not receive the write; got hits=%+v", hits)
	}
}

// TestStore_SetMirror_ScopeIsMemType verifies the mirror uses the memstore
// memType as the memory.Scope — so a "preference" write goes into scope
// "preference", not "fact". Readers depend on this to query the right
// scope post-migration.
func TestStore_SetMirror_ScopeIsMemType(t *testing.T) {
	mirror, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer mirror.Close()

	s, err := memstore.New(filepath.Join(t.TempDir(), "m.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetMirror(mirror)

	if _, err := s.Store("user prefers dark mode", "preference", "extractor", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store("project uses Go 1.24", "fact", "extractor", nil); err != nil {
		t.Fatal(err)
	}

	prefHits, _ := mirror.Search(context.Background(), "preference", "dark mode", 5)
	if len(prefHits) == 0 {
		t.Errorf("preference not mirrored into scope='preference'")
	}
	factHits, _ := mirror.Search(context.Background(), "fact", "Go 1.24", 5)
	if len(factHits) == 0 {
		t.Errorf("fact not mirrored into scope='fact'")
	}
	// Cross-scope leak check: the preference must NOT be in scope='fact'.
	leakHits, _ := mirror.Search(context.Background(), "fact", "dark mode", 5)
	for _, h := range leakHits {
		if h.Document.Text == "user prefers dark mode" {
			t.Errorf("preference leaked into scope='fact'")
		}
	}
}
