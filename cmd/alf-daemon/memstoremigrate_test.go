package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/alamparelli/alf/internal/memory"
)

// seedMemstore fabricates a legacy memstore.db with the old `memories`
// schema. The real memstore.Store has been retired (#337 close-out) so
// the test speaks directly to SQLite to build the file the migrator
// expects on disk.
func seedMemstore(t *testing.T, contextDir string) {
	t.Helper()
	dbPath := filepath.Join(contextDir, "memory.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open legacy memstore: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			text TEXT NOT NULL,
			type TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'extractor',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create memories table: %v", err)
	}

	seeds := []struct{ text, typ, source string }{
		{"the deployment uses docker compose on homelab", "fact", "extractor"},
		{"user prefers dark mode for all applications", "preference", "extractor"},
		{"decided to use sqlite-vec for vector storage", "decision", "extractor"},
		{"contact John Doe +1-555-0100", "contact", "extractor"},
	}
	now := time.Now().Format(time.RFC3339)
	for _, s := range seeds {
		if _, err := db.Exec(
			`INSERT INTO memories (text, type, source, metadata, created_at) VALUES (?, ?, ?, '{}', ?)`,
			s.text, s.typ, s.source, now,
		); err != nil {
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
