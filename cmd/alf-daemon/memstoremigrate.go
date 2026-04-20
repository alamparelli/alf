package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/alamparelli/alf/internal/memory"
)

// DEPRECATED — scheduled removal target: v0.7.14 (same cycle as
// migrateChatDBToMemoryDB — see memorymigrate.go).
//
// migrateMemstoreToMemory is the #337c3 one-shot importer. It copies the
// contents of the legacy memstore database (contextDir/memory.db, table
// `memories`) into the unified memory.Store's `documents` table so recall
// — which now reads from memory.Store (#337c2) — sees every pre-existing
// fact the user has built up.
//
// Idempotent: a sentinel file (contextDir/.memstore_migrated) records
// completion so subsequent boots are a no-op. The legacy DB is NOT
// renamed because memstore itself still opens it for the C1 dual-write
// shim — both paths coexist until #337c4 retires memstore.
//
// Safety:
//   - Each row is written via memory.Store.Index(scope=type,
//     doc_id=fmt.Sprint(id)); the store's ON CONFLICT(scope, doc_id) DO
//     UPDATE means a row already mirrored by C1 is refreshed, not
//     duplicated.
//   - Errors on individual rows are logged and skipped, not fatal —
//     one malformed memory must not black-hole the migration of the
//     other 99%.
//   - The sentinel is written only after the full pass completes so a
//     crashed migration re-runs cleanly on the next boot.
func migrateMemstoreToMemory(ctx context.Context, contextDir string, memStore memory.Store) error {
	legacyPath := filepath.Join(contextDir, "memory.db")
	sentinel := filepath.Join(contextDir, ".memstore_migrated")

	if _, err := os.Stat(sentinel); err == nil {
		return nil
	}
	if _, err := os.Stat(legacyPath); err != nil {
		if os.IsNotExist(err) {
			// Nothing to migrate. Write sentinel so we don't keep
			// probing the filesystem on every boot.
			return writeSentinel(sentinel)
		}
		return fmt.Errorf("stat legacy memstore.db: %w", err)
	}

	// Open read-only. WAL + busy_timeout match the pragmas memstore itself
	// uses — means the live handle that memstore holds will not block us
	// (readers never conflict under WAL).
	legacyDB, err := sql.Open("sqlite3", legacyPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open legacy memstore.db: %w", err)
	}
	defer legacyDB.Close()

	// If there's no `memories` table (e.g. the file is a fresh chat.db that
	// got stray-named into contextDir), nothing to do.
	var has int
	if err := legacyDB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memories'`,
	).Scan(&has); err != nil || has == 0 {
		return writeSentinel(sentinel)
	}

	rows, err := legacyDB.QueryContext(ctx, `
		SELECT id, text, type, source, metadata, created_at
		FROM memories
		ORDER BY id ASC`)
	if err != nil {
		return fmt.Errorf("read memories: %w", err)
	}
	defer rows.Close()

	var imported, skipped int64
	start := time.Now()
	for rows.Next() {
		var id int64
		var text, memType, source, metaJSON, createdAt string
		if err := rows.Scan(&id, &text, &memType, &source, &metaJSON, &createdAt); err != nil {
			log.Printf("[memstore-migrate] scan row: %v", err)
			skipped++
			continue
		}
		meta := map[string]string{"source": source, "created_at": createdAt}
		// Flatten the original JSON metadata into string values — memory
		// Document metadata is map[string]string. Non-string values are
		// re-encoded so nothing is silently dropped.
		if metaJSON != "" && metaJSON != "{}" {
			var raw map[string]any
			if json.Unmarshal([]byte(metaJSON), &raw) == nil {
				for k, v := range raw {
					switch vv := v.(type) {
					case string:
						meta[k] = vv
					default:
						if b, err := json.Marshal(v); err == nil {
							meta[k] = string(b)
						}
					}
				}
			}
		}
		if err := memStore.Index(ctx, memory.Scope(memType), memory.Document{
			ID:       fmt.Sprintf("%d", id),
			Text:     text,
			Metadata: meta,
		}); err != nil {
			log.Printf("[memstore-migrate] Index memory #%d scope=%q: %v", id, memType, err)
			skipped++
			continue
		}
		imported++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate memories: %w", err)
	}

	if err := writeSentinel(sentinel); err != nil {
		log.Printf("[memstore-migrate] WARNING: import done but sentinel write failed: %v (next boot will re-run; Index is upsert-safe)", err)
	}

	log.Printf("[memstore-migrate] imported %d memories (skipped %d) from %s in %s",
		imported, skipped, legacyPath, time.Since(start).Round(time.Millisecond))
	return nil
}

// writeSentinel drops a zero-byte marker file to record migration completion.
// The content doesn't matter — only the file's existence is consulted.
func writeSentinel(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
