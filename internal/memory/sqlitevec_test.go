package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/memtest"
)

// TestSQLiteStore_VecSearch_ReturnsClosestMatch verifies the semantic
// Search path: with an embedder wired, Search should rank documents by
// vector similarity and return the closest-matching doc first.
func TestSQLiteStore_VecSearch_ReturnsClosestMatch(t *testing.T) {
	emb := memtest.NewStubEmbedder(32)
	s, err := memory.NewSQLiteStore(t.TempDir(), memory.WithEmbedder(emb))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	docs := []memory.Document{
		{ID: "a", Text: "the cat sat on the mat"},
		{ID: "b", Text: "quantum entanglement and superposition"},
		{ID: "c", Text: "a dog ran across the yard"},
	}
	for _, d := range docs {
		if err := s.Index(ctx, "test", d); err != nil {
			t.Fatalf("Index %q: %v", d.ID, err)
		}
	}

	hits, err := s.Search(ctx, "test", "cat mat", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("Search returned no hits")
	}
	if hits[0].Document.ID != "a" {
		t.Errorf("expected top hit = 'a' (exact token overlap), got %q (score=%f)", hits[0].Document.ID, hits[0].Score)
	}
}

// TestSQLiteStore_VecSearch_ScopeIsolation verifies that Search only
// returns docs indexed under the requested scope, even when other scopes
// contain a better vector match.
func TestSQLiteStore_VecSearch_ScopeIsolation(t *testing.T) {
	emb := memtest.NewStubEmbedder(32)
	s, err := memory.NewSQLiteStore(t.TempDir(), memory.WithEmbedder(emb))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Index(ctx, "scopeA", memory.Document{ID: "1", Text: "alpha beta gamma"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Index(ctx, "scopeB", memory.Document{ID: "2", Text: "alpha beta gamma"}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search(ctx, "scopeA", "alpha beta", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Document.ID != "1" {
			t.Errorf("scopeA search leaked doc %q from another scope", h.Document.ID)
		}
	}
}

// TestSQLiteStore_VecIndex_UpdatesReplaceVector verifies re-indexing the
// same (scope, doc_id) refreshes the vector rather than stacking two rows.
func TestSQLiteStore_VecIndex_UpdatesReplaceVector(t *testing.T) {
	emb := memtest.NewStubEmbedder(32)
	s, err := memory.NewSQLiteStore(t.TempDir(), memory.WithEmbedder(emb))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	_ = s.Index(ctx, "s", memory.Document{ID: "x", Text: "first version about oranges"})
	_ = s.Index(ctx, "s", memory.Document{ID: "x", Text: "replacement about apples"})

	hits, err := s.Search(ctx, "s", "apples", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit after re-index, got %d", len(hits))
	}
	if !strings.Contains(hits[0].Document.Text, "apples") {
		t.Errorf("expected updated text, got %q", hits[0].Document.Text)
	}
}

// TestSQLiteStore_NoEmbedder_FallsBackToLike guards the backwards-compatible
// path: callers that don't pass WithEmbedder still get a working Search via
// the LIKE fallback — critical for tests and bootstrap runs.
func TestSQLiteStore_NoEmbedder_FallsBackToLike(t *testing.T) {
	s, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	_ = s.Index(ctx, "s", memory.Document{ID: "1", Text: "needle in a haystack"})
	_ = s.Index(ctx, "s", memory.Document{ID: "2", Text: "unrelated content"})

	hits, err := s.Search(ctx, "s", "needle", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Document.ID != "1" {
		t.Errorf("LIKE fallback: expected single hit id=1, got %+v", hits)
	}
}

// TestSQLiteStore_DeleteDocument_RemovesVecRow catches the failure mode
// where DeleteDocument cleans the base row and FTS trigger fires, but
// documents_vec keeps an orphan embedding that still matches in Search.
// Exercises the vec path specifically — the generic contract test only
// hits the LIKE fallback because it runs without an embedder.
func TestSQLiteStore_DeleteDocument_RemovesVecRow(t *testing.T) {
	emb := memtest.NewStubEmbedder(32)
	s, err := memory.NewSQLiteStore(t.TempDir(), memory.WithEmbedder(emb))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Index(ctx, "s", memory.Document{ID: "v1", Text: "apple orange banana"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Index(ctx, "s", memory.Document{ID: "v2", Text: "apple mango kiwi"}); err != nil {
		t.Fatal(err)
	}

	// Precondition: vec search returns both.
	pre, _ := s.Search(ctx, "s", "apple", 5)
	if len(pre) != 2 {
		t.Fatalf("precondition: expected 2 hits, got %d", len(pre))
	}

	ok, err := s.DeleteDocument(ctx, "s", "v1")
	if err != nil || !ok {
		t.Fatalf("DeleteDocument: ok=%v err=%v", ok, err)
	}

	// Search must now only surface v2 — no orphan vec hit for v1.
	hits, err := s.Search(ctx, "s", "apple", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Document.ID == "v1" {
			t.Errorf("vec search returned deleted doc v1: score=%f", h.Score)
		}
	}
	if len(hits) == 0 {
		t.Error("expected v2 to remain, got 0 hits")
	}
}

// TestSQLiteStore_VecDim_PersistsAcrossOpens catches the schema-drift
// failure mode where a store is re-opened with an embedder of a different
// dimension. The second open must fail fast rather than silently corrupting
// the vec index.
func TestSQLiteStore_VecDim_PersistsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	s1, err := memory.NewSQLiteStore(dir, memory.WithEmbedder(memtest.NewStubEmbedder(16)))
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	_, err = memory.NewSQLiteStore(dir, memory.WithEmbedder(memtest.NewStubEmbedder(32)))
	if err == nil {
		t.Fatalf("expected dim-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "dim mismatch") {
		t.Errorf("expected dim mismatch error, got: %v", err)
	}
}
