package dedup_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/dedup"
	"github.com/alamparelli/alf/internal/memory/memtest"
)

// newStore builds a fresh SQLiteStore. Some tests need an embedder for
// vec-backed near-dup; opt in via the variant below.
func newStore(t *testing.T) memory.Store {
	t.Helper()
	s, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newStoreWithEmbedder(t *testing.T, dim int) memory.Store {
	t.Helper()
	s, err := memory.NewSQLiteStore(t.TempDir(), memory.WithEmbedder(memtest.NewStubEmbedder(dim)))
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestIndexWithDedup_ExactDupIsNoOp confirms the core contract: indexing
// the same text twice in the same scope yields one stored document and
// the second call reports Reason="exact".
func TestIndexWithDedup_ExactDupIsNoOp(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	r1, err := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "project uses Go 1.24"}, dedup.Options{Source: "extractor"})
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Stored {
		t.Errorf("first call should store; got %+v", r1)
	}

	r2, err := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "project uses Go 1.24"}, dedup.Options{Source: "extractor"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Stored {
		t.Errorf("second call should skip; got %+v", r2)
	}
	if r2.Reason != "exact" {
		t.Errorf("expected Reason=exact, got %q", r2.Reason)
	}
	if r2.DocID != r1.DocID {
		t.Errorf("DocID should be stable across calls: %q vs %q", r1.DocID, r2.DocID)
	}
}

// TestIndexWithDedup_DocIDIsStable pins the DocID derivation so existing
// installs can rely on hash stability across releases.
func TestIndexWithDedup_DocIDIsStable(t *testing.T) {
	a := dedup.DocID("alpha")
	b := dedup.DocID("alpha")
	c := dedup.DocID("beta")
	if a != b {
		t.Errorf("DocID not deterministic: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("DocID collides for distinct text: %q", a)
	}
	if !strings.HasPrefix(a, "mem-") {
		t.Errorf("DocID should start with mem-: %q", a)
	}
	// 4 chars for "mem-" + 24 hex chars for 12 bytes.
	if len(a) != 4+24 {
		t.Errorf("DocID length changed: got %d, want 28", len(a))
	}
}

// TestIndexWithDedup_NearDupBlocks verifies the embedder-backed
// semantic dedup path. Stored text and similar-but-not-identical query
// share enough tokens that the stub embedder (hashed-BOW) rates them
// close, and the threshold check blocks the near-dup write.
func TestIndexWithDedup_NearDupBlocks(t *testing.T) {
	s := newStoreWithEmbedder(t, 32)
	ctx := context.Background()

	first := "user prefers dark mode for coding"
	_, err := dedup.IndexWithDedup(ctx, s, "preference", memory.Document{Text: first}, dedup.Options{Source: "extractor"})
	if err != nil {
		t.Fatal(err)
	}

	// Same bag of tokens plus one new word — vec cosine ends up high.
	similar := "user prefers dark mode for coding sessions"
	r, err := dedup.IndexWithDedup(ctx, s, "preference", memory.Document{Text: similar}, dedup.Options{
		Source:           "extractor",
		NearDupThreshold: 0.75,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stored {
		t.Errorf("near-dup should be skipped; got %+v", r)
	}
	if r.Reason != "near" {
		t.Errorf("expected Reason=near, got %q", r.Reason)
	}
	if r.Near == nil || r.Near.Document.Text != first {
		t.Errorf("near hit should reference the first write; got %+v", r.Near)
	}
}

// TestIndexWithDedup_NearDupThresholdZeroDisables guards the opt-in
// semantic: callers who leave NearDupThreshold=0 must get exact-only
// dedup and writes of near-duplicate text must go through.
func TestIndexWithDedup_NearDupThresholdZeroDisables(t *testing.T) {
	s := newStoreWithEmbedder(t, 32)
	ctx := context.Background()

	_, _ = dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "the cat sat on the mat"}, dedup.Options{})
	r, err := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "the cat sat on the rug"}, dedup.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stored {
		t.Errorf("near-dup should be allowed when threshold=0; got %+v", r)
	}
}

// TestIndexWithDedup_ScopeIsolation catches the failure mode where a
// "fact" and a "preference" with identical text collapse into one row
// because dedup ignored scope.
func TestIndexWithDedup_ScopeIsolation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	text := "alpha beta gamma"

	r1, _ := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: text}, dedup.Options{})
	r2, _ := dedup.IndexWithDedup(ctx, s, "preference", memory.Document{Text: text}, dedup.Options{})
	if !r1.Stored || !r2.Stored {
		t.Errorf("same text in different scopes should store twice; r1=%+v r2=%+v", r1, r2)
	}
}

// TestIndexWithDedup_PopulatesMetadata ensures Source + created_at land
// on the stored Document so downstream consumers see the same shape
// memstore.Store used to produce.
func TestIndexWithDedup_PopulatesMetadata(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := func() string { return "2026-04-20T08:00:00Z" }
	r, err := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "populate me"}, dedup.Options{
		Source: "extractor",
		Now:    now,
	})
	if err != nil || !r.Stored {
		t.Fatalf("store: %+v err=%v", r, err)
	}
	got, _ := s.GetDocument(ctx, "fact", r.DocID)
	if got == nil {
		t.Fatal("stored doc missing")
	}
	if got.Metadata["source"] != "extractor" {
		t.Errorf("source = %q, want extractor", got.Metadata["source"])
	}
	if got.Metadata["created_at"] != "2026-04-20T08:00:00Z" {
		t.Errorf("created_at injected by Now() not preserved: %q", got.Metadata["created_at"])
	}
}

// TestIndexWithDedup_RespectsCallerMetadata verifies that caller-supplied
// metadata survives — dedup must not clobber keys the caller already
// set (matches the AppendPreference metadata pattern in comms).
func TestIndexWithDedup_RespectsCallerMetadata(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	doc := memory.Document{
		Text: "respect my metadata",
		Metadata: map[string]string{
			"custom":     "keepme",
			"created_at": "pre-existing",
		},
	}
	r, err := dedup.IndexWithDedup(ctx, s, "fact", doc, dedup.Options{
		Source: "extractor",
		Now:    func() string { return "2026-04-20T08:00:00Z" },
	})
	if err != nil || !r.Stored {
		t.Fatalf("store: %+v err=%v", r, err)
	}
	got, _ := s.GetDocument(ctx, "fact", r.DocID)
	if got.Metadata["custom"] != "keepme" {
		t.Errorf("custom metadata lost: %+v", got.Metadata)
	}
	if got.Metadata["created_at"] != "pre-existing" {
		t.Errorf("caller's created_at should win over opts.Now; got %q", got.Metadata["created_at"])
	}
	if got.Metadata["source"] != "extractor" {
		t.Errorf("source overwrite failed")
	}
}

// TestIndexWithDedup_RejectsEmpty guards the defensive error path.
func TestIndexWithDedup_RejectsEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := dedup.IndexWithDedup(ctx, nil, "s", memory.Document{Text: "x"}, dedup.Options{}); err == nil {
		t.Error("expected error for nil store")
	}
	if _, err := dedup.IndexWithDedup(ctx, s, "", memory.Document{Text: "x"}, dedup.Options{}); err == nil {
		t.Error("expected error for empty scope")
	}
	if _, err := dedup.IndexWithDedup(ctx, s, "f", memory.Document{Text: ""}, dedup.Options{}); err == nil {
		t.Error("expected error for empty text")
	}
	if _, err := dedup.IndexWithDedup(ctx, s, "f", memory.Document{Text: "   "}, dedup.Options{}); err == nil {
		t.Error("expected error for whitespace-only text")
	}
}

// TestIndexWithDedup_ConcurrentIdenticalTextConvergeOnOneRow stresses
// the exact-dup path: N goroutines race to index the same text; the
// final state must have exactly one row, not N. If the GetDocument /
// Index pair were not atomic at the SQLite level this would produce
// duplicates or errors.
func TestIndexWithDedup_ConcurrentIdenticalTextConvergeOnOneRow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	var stored, skipped atomic.Int32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "shared by all"}, dedup.Options{Source: "extractor"})
			if err != nil {
				errs <- err
				return
			}
			if r.Stored {
				stored.Add(1)
			} else {
				skipped.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent: %v", err)
	}

	// At least one goroutine must have stored; the rest must have seen
	// it as a duplicate. Exactly-one-writer is too strict for this
	// check-then-write pattern (race window between GetDocument and
	// Index), but the upsert semantic of Index means the final row count
	// is always 1.
	if stored.Load() < 1 {
		t.Errorf("at least one goroutine should have stored; got %d stored / %d skipped", stored.Load(), skipped.Load())
	}

	id := dedup.DocID("shared by all")
	got, _ := s.GetDocument(ctx, "fact", id)
	if got == nil {
		t.Fatal("document missing after concurrent writes")
	}
	if got.Text != "shared by all" {
		t.Errorf("corrupted text: %q", got.Text)
	}

	// Count via search: must be exactly 1 row.
	hits, _ := s.Search(ctx, "fact", "shared by all", 100)
	if len(hits) != 1 {
		t.Errorf("expected exactly 1 row in fact scope, got %d", len(hits))
	}
}

// TestIndexWithDedup_NearDupWithoutEmbedderFallsBackToLike documents
// the degraded-dedup semantics when no embedder is configured.
// memory.Store's LIKE fallback only flags contains-substring matches,
// so near-dup catches the case where the second text is a prefix/
// substring of the first — enough for a dev-mode guardrail, nothing
// close to the FTS5/vec quality of the embedder path.
func TestIndexWithDedup_NearDupWithoutEmbedderFallsBackToLike(t *testing.T) {
	s := newStore(t) // no embedder
	ctx := context.Background()

	// Store the longer phrase first; the second write with a substring
	// of it triggers the LIKE match inside memory.Search.
	_, _ = dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "Brussels is the capital of Belgium"}, dedup.Options{})
	r, err := dedup.IndexWithDedup(ctx, s, "fact", memory.Document{Text: "Brussels is the capital"}, dedup.Options{
		NearDupThreshold: 0.01, // LIKE scores are tiny; threshold must be too
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stored {
		t.Errorf("LIKE-based near-dup (substring match) should skip; got %+v", r)
	}
	if r.Reason != "near" {
		t.Errorf("expected Reason=near, got %q", r.Reason)
	}
}

