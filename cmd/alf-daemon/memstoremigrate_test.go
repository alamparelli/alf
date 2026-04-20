package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memstore"
)

// seedMemstore opens a memstore in contextDir and writes a handful of
// typed memories. Returns the directory so the caller can point the
// migration at it.
func seedMemstore(t *testing.T, contextDir string) {
	t.Helper()
	store, err := memstore.New(filepath.Join(contextDir, "memory.db"), nil)
	if err != nil {
		t.Fatalf("memstore.New: %v", err)
	}
	defer store.Close()

	seeds := []struct{ text, typ, source string }{
		{"the deployment uses docker compose on homelab", "fact", "extractor"},
		{"user prefers dark mode for all applications", "preference", "extractor"},
		{"decided to use sqlite-vec for vector storage", "decision", "extractor"},
		{"contact John Doe +1-555-0100", "contact", "extractor"},
	}
	for _, s := range seeds {
		if _, err := store.Store(s.text, s.typ, s.source, nil); err != nil {
			t.Fatalf("seed %q: %v", s.text, err)
		}
	}
}

// TestMigrateMemstoreToMemory_CopiesAllTypes verifies the migrator imports
// every memstore memory into memory.Store under its memType as Scope, and
// that scope-isolated Search returns each one afterwards.
func TestMigrateMemstoreToMemory_CopiesAllTypes(t *testing.T) {
	dataDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedMemstore(t, contextDir)

	memStore, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	defer memStore.Close()

	ctx := context.Background()
	if err := migrateMemstoreToMemory(ctx, contextDir, memStore); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Each seeded memory must be retrievable from its scope.
	cases := []struct {
		scope, query string
	}{
		{"fact", "docker compose"},
		{"preference", "dark mode"},
		{"decision", "sqlite-vec"},
		{"contact", "John Doe"},
	}
	for _, tc := range cases {
		hits, err := memStore.Search(ctx, memory.Scope(tc.scope), tc.query, 5)
		if err != nil {
			t.Errorf("Search scope=%q: %v", tc.scope, err)
			continue
		}
		if len(hits) == 0 {
			t.Errorf("Search scope=%q query=%q: no hits", tc.scope, tc.query)
			continue
		}
		if hits[0].Document.Metadata["source"] != "extractor" {
			t.Errorf("scope=%q: expected source='extractor', got %q", tc.scope, hits[0].Document.Metadata["source"])
		}
	}
}

// TestMigrateMemstoreToMemory_IsIdempotent confirms the sentinel gates
// re-runs so repeated boots don't re-import and don't crash.
func TestMigrateMemstoreToMemory_IsIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedMemstore(t, contextDir)

	memStore, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer memStore.Close()

	ctx := context.Background()
	if err := migrateMemstoreToMemory(ctx, contextDir, memStore); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := migrateMemstoreToMemory(ctx, contextDir, memStore); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// Sentinel must exist after the first run.
	if _, err := os.Stat(filepath.Join(contextDir, ".memstore_migrated")); err != nil {
		t.Errorf("sentinel missing: %v", err)
	}
}

// TestMigrateMemstoreToMemory_NoLegacyDB verifies the migration is a
// no-op on a fresh install that has never had memstore.
func TestMigrateMemstoreToMemory_NoLegacyDB(t *testing.T) {
	dataDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}

	memStore, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer memStore.Close()

	if err := migrateMemstoreToMemory(context.Background(), contextDir, memStore); err != nil {
		t.Fatalf("no-op migration returned error: %v", err)
	}
	// Sentinel must still be dropped so subsequent boots skip.
	if _, err := os.Stat(filepath.Join(contextDir, ".memstore_migrated")); err != nil {
		t.Errorf("fresh-install sentinel missing: %v", err)
	}
}
