package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
)

// newTestMemoryStore returns an isolated memory.Store for adapter tests.
// Uses an on-disk DB under t.TempDir() because the embedder-off path
// still exercises SQLite behaviour that :memory: won't replicate
// identically (e.g. WAL + busy_timeout pragmas).
func newTestMemoryStore(t *testing.T) memory.Store {
	t.Helper()
	s, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestMemoryIngestAdapter_StoresUnderScope verifies that a Store() call
// lands as an Index under scope=memType and that the text is retrievable
// via Search on that same scope.
func TestMemoryIngestAdapter_StoresUnderScope(t *testing.T) {
	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}

	if _, err := a.Store("user likes dark themes", "preference", "user-import", nil); err != nil {
		t.Fatalf("Store: %v", err)
	}

	ctx := context.Background()
	hits, err := store.Search(ctx, "preference", "dark themes", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if !strings.Contains(hits[0].Document.Text, "dark themes") {
		t.Errorf("round-trip lost text: %q", hits[0].Document.Text)
	}
	if hits[0].Document.Metadata["source"] != "user-import" {
		t.Errorf("metadata.source = %q, want %q", hits[0].Document.Metadata["source"], "user-import")
	}
	if hits[0].Document.Metadata["created_at"] == "" {
		t.Errorf("metadata.created_at should be populated")
	}
}

// TestMemoryIngestAdapter_IdempotentOnIdenticalText catches the dedup
// semantic: two Store() calls with identical text must produce the same
// doc_id (sha256-derived), so the documents table holds exactly one row.
func TestMemoryIngestAdapter_IdempotentOnIdenticalText(t *testing.T) {
	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}

	text := "project runs on Go 1.24 with sqlite-vec"
	if _, err := a.Store(text, "fact", "user-import", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store(text, "fact", "user-import", nil); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	hits, _ := store.Search(ctx, "fact", "Go 1.24", 10)
	if len(hits) != 1 {
		t.Errorf("expected idempotent upsert (1 row), got %d hits with text=%q", len(hits), text)
	}
}

// TestMemoryIngestAdapter_ScopeIsolation verifies that the same text
// ingested under two different memTypes lands as two distinct documents
// (different scopes), not one — scopes are independent namespaces.
func TestMemoryIngestAdapter_ScopeIsolation(t *testing.T) {
	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}

	text := "alpha beta gamma"
	if _, err := a.Store(text, "fact", "user-import", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store(text, "preference", "user-import", nil); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	fHits, _ := store.Search(ctx, "fact", "alpha", 5)
	pHits, _ := store.Search(ctx, "preference", "alpha", 5)
	if len(fHits) != 1 || len(pHits) != 1 {
		t.Errorf("expected 1 hit in each scope, got fact=%d preference=%d", len(fHits), len(pHits))
	}
}

// TestMemoryIngestAdapter_DocIDIsStable pins the doc_id derivation so the
// idempotency story stays reproducible across releases — if the hash
// strategy changes, existing installs would see duplicates on re-ingest.
func TestMemoryIngestAdapter_DocIDIsStable(t *testing.T) {
	text := "the cat sat on the mat"
	h := sha256.Sum256([]byte(text))
	want := fmt.Sprintf("ingest-%x", h[:12])

	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}
	if _, err := a.Store(text, "fact", "user-import", nil); err != nil {
		t.Fatal(err)
	}
	// Search by known scope and confirm doc_id matches the formula.
	ctx := context.Background()
	hits, _ := store.Search(ctx, "fact", "cat", 5)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Document.ID != want {
		t.Errorf("doc_id changed: got %q, want %q", hits[0].Document.ID, want)
	}
}

// TestMemoryIngestAdapter_NilMetaIsFine guards a regression mode where
// the adapter mishandled a nil meta arg from handler_memory.go's
// storeAsIs path. The built-in source/created_at keys must still land.
func TestMemoryIngestAdapter_NilMetaIsFine(t *testing.T) {
	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}

	if _, err := a.Store("nil meta passes", "fact", "user-import", nil); err != nil {
		t.Fatalf("Store with nil meta: %v", err)
	}
	ctx := context.Background()
	hits, _ := store.Search(ctx, "fact", "nil meta", 5)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Document.Metadata["source"] == "" {
		t.Errorf("source metadata dropped with nil input meta")
	}
}

// TestMemoryIngestAdapter_NonStringMetaIsJSONSerialised confirms the
// adapter does not silently drop non-string meta values — they are
// JSON-encoded so downstream consumers can recover them.
func TestMemoryIngestAdapter_NonStringMetaIsJSONSerialised(t *testing.T) {
	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}

	meta := map[string]any{
		"stringy": "keepasis",
		"numeric": 42,
		"arr":     []string{"a", "b"},
	}
	if _, err := a.Store("serialise me", "fact", "user-import", meta); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hits, _ := store.Search(ctx, "fact", "serialise", 5)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	md := hits[0].Document.Metadata
	if md["stringy"] != "keepasis" {
		t.Errorf("string value lost: %q", md["stringy"])
	}
	// Numeric → JSON number string.
	if md["numeric"] != "42" {
		t.Errorf("numeric meta: got %q, want %q", md["numeric"], "42")
	}
	// Array → JSON array string.
	var got []string
	if err := json.Unmarshal([]byte(md["arr"]), &got); err != nil || len(got) != 2 {
		t.Errorf("arr meta did not round-trip as JSON: raw=%q err=%v", md["arr"], err)
	}
}

// TestMemoryIngestAdapter_ConcurrentStoresAreSafe exercises the adapter
// under parallel writers — the memory.Store backend serializes writes
// under a single connection, so this is really a race-detector smoke
// test for the adapter wrapper itself and the SHA-256 / metadata
// derivation path.
func TestMemoryIngestAdapter_ConcurrentStoresAreSafe(t *testing.T) {
	store := newTestMemoryStore(t)
	a := &memoryIngestAdapter{store: store}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := fmt.Sprintf("concurrent fact number %d about testing", i)
			if _, err := a.Store(text, "fact", "user-import", nil); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Store: %v", err)
	}

	// Every row must be independently retrievable.
	ctx := context.Background()
	for i := 0; i < n; i++ {
		q := fmt.Sprintf("number %d", i)
		hits, err := store.Search(ctx, "fact", q, 5)
		if err != nil {
			t.Errorf("Search %q: %v", q, err)
			continue
		}
		if len(hits) == 0 {
			t.Errorf("row %d missing after concurrent Store", i)
		}
	}
}

// TestMemoryIngestAdapter_NilStoreReturnsError guards the bootstrap
// mistake of wiring the adapter before the memory.Store is open — the
// caller must get a clear error instead of a nil-pointer panic.
func TestMemoryIngestAdapter_NilStoreReturnsError(t *testing.T) {
	a := &memoryIngestAdapter{store: nil}
	_, err := a.Store("anything", "fact", "user-import", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
