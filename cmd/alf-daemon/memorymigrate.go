package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DEPRECATED — scheduled removal target: v0.7.14.
//
// This file is a one-shot upgrade helper introduced in v0.7.9 (#336)
// to move users off the legacy internal/chatdb store into the unified
// memory.Store. Every install that boots v0.7.9+ once will have its
// chat.db renamed to chat.db.migrated — a no-op sentinel recognised on
// subsequent boots.
//
// Removal plan:
//
//  1. Give the migration 5 minor-release cycles to run everywhere
//     (v0.7.9 → v0.7.14). After that point any install still holding
//     an un-migrated chat.db is either offline for >6 months or
//     broken in other ways that this file won't fix.
//
//  2. When the version bump to v0.7.14 lands, delete this file,
//     memorymigrate_test.go, and the migrateChatDBToMemoryDB call
//     in main.go. Any remaining chat.db files become dead artifacts
//     on disk that operators can delete manually.
//
//  3. A reminder: `grep -rn migrationTargetRemovalVersion` — the
//     constant below makes this file trivially findable by grep or
//     by any future "deprecation audit" pass.
const migrationTargetRemovalVersion = "v0.7.14"

// migrateChatDBToMemoryDB imports a legacy chatdb file (dataDir/logs/chat.db)
// into the new unified memory.db (dataDir/memory.db).
//
// Called on boot before NewSQLiteStore opens memory.db. Safe to call multiple
// times — detects a sentinel (chat.db.migrated) and returns nil on second run.
// Atomic: the entire copy runs in one transaction on memory.db; on any error
// the transaction rolls back and chat.db is left untouched so the operator
// can retry.
//
// Conversions performed:
//
//   - conversations.source → conversations.channel
//   - messages.text        → messages.content
//   - messages.source      → messages.channel
//   - DATETIME columns     → int64 unix millis (columns: conversations.created_at,
//     updated_at; messages.created_at)
//   - kv_meta              → prefs (JSON-wrapped values)
//
// IDs, per-conv seq numbers, and reaction/media/block relationships are
// preserved byte-for-byte so any external references (UI bookmarks, session
// tracking, saved links) keep working.
func migrateChatDBToMemoryDB(dataDir string) error {
	legacyPath := filepath.Join(dataDir, "logs", "chat.db")
	migratedMark := legacyPath + ".migrated"
	newPath := filepath.Join(dataDir, "memory.db")

	if _, err := os.Stat(migratedMark); err == nil {
		// Already migrated on a previous boot.
		return nil
	}
	legacyInfo, err := os.Stat(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing to migrate — fresh install.
			return nil
		}
		return fmt.Errorf("stat legacy chat.db: %w", err)
	}
	if legacyInfo.Size() == 0 {
		// Empty file — nothing to move. Still mark it so we don't retry.
		return os.Rename(legacyPath, migratedMark)
	}

	// Open legacy DB read-only.
	legacyDB, err := sql.Open("sqlite3", legacyPath+"?mode=ro&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open legacy chat.db: %w", err)
	}
	defer legacyDB.Close()

	// Open new DB with the same pragmas as memory.NewSQLiteStore uses.
	// We write via raw SQL (not memory.Store) to preserve IDs and seq exactly.
	newDB, err := sql.Open("sqlite3", newPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1")
	if err != nil {
		return fmt.Errorf("open new memory.db: %w", err)
	}
	defer newDB.Close()
	newDB.SetMaxOpenConns(1)

	// Bootstrap the schema by running the same DDL memory.NewSQLiteStore does.
	// Idempotent — CREATE TABLE IF NOT EXISTS throughout.
	if _, err := newDB.Exec(memoryDBSchema); err != nil {
		return fmt.Errorf("create memory.db schema: %w", err)
	}

	// Sanity check: refuse to migrate into a non-empty messages table; that
	// would mean some other process already populated memory.db and we would
	// risk ID collisions. The operator must resolve manually.
	var existingMsgs int64
	if err := newDB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&existingMsgs); err != nil {
		return fmt.Errorf("count existing messages: %w", err)
	}
	if existingMsgs > 0 {
		return fmt.Errorf("memory.db already contains %d messages — refusing to merge with legacy chat.db (%s); rename or remove one of them and retry", existingMsgs, legacyPath)
	}

	tx, err := newDB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stats := migrationStats{}

	if err := migrateConversations(legacyDB, tx, &stats); err != nil {
		return fmt.Errorf("migrate conversations: %w", err)
	}
	if err := migrateMessages(legacyDB, tx, &stats); err != nil {
		return fmt.Errorf("migrate messages: %w", err)
	}
	if err := migrateContentBlocks(legacyDB, tx, &stats); err != nil {
		return fmt.Errorf("migrate content_blocks: %w", err)
	}
	if err := migrateReactions(legacyDB, tx, &stats); err != nil {
		return fmt.Errorf("migrate reactions: %w", err)
	}
	if err := migrateMedia(legacyDB, tx, &stats); err != nil {
		return fmt.Errorf("migrate media: %w", err)
	}
	if err := migrateKVMeta(legacyDB, tx, &stats); err != nil {
		return fmt.Errorf("migrate kv_meta: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// Rename the legacy file as a sentinel so subsequent boots skip.
	if err := os.Rename(legacyPath, migratedMark); err != nil {
		log.Printf("[memory-migrate] WARNING: copy succeeded but renaming chat.db failed: %v (copy is durable; next boot may re-attempt)", err)
	}

	log.Printf("[memory-migrate] imported from %s into %s: %d convs, %d messages, %d blocks, %d reactions, %d media, %d prefs",
		legacyPath, newPath,
		stats.convs, stats.messages, stats.blocks, stats.reactions, stats.media, stats.prefs)
	return nil
}

type migrationStats struct {
	convs, messages, blocks, reactions, media, prefs int64
}

// memoryDBSchema is duplicated from internal/memory/sqlite.go to avoid cmd/
// importing private schema constants. Kept in sync by the ports test below
// (see migration_test.go).
const memoryDBSchema = `
CREATE TABLE IF NOT EXISTS conversations (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL DEFAULT '',
    channel    TEXT NOT NULL DEFAULT '',
    archived   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    id          TEXT PRIMARY KEY,
    conv_id     TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    role        TEXT NOT NULL,
    channel     TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    tier        TEXT NOT NULL DEFAULT '',
    backend     TEXT NOT NULL DEFAULT '',
    cost_usd    REAL NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    session_id  TEXT NOT NULL DEFAULT '',
    reply_to    TEXT NOT NULL DEFAULT '',
    tool_call   TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_conv_seq     ON messages(conv_id, seq);
CREATE INDEX IF NOT EXISTS idx_messages_conv_created ON messages(conv_id, created_at);

CREATE TABLE IF NOT EXISTS content_blocks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id  TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    block_index INTEGER NOT NULL,
    block_type  TEXT NOT NULL,
    text        TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    input       TEXT NOT NULL DEFAULT '',
    tool_id     TEXT NOT NULL DEFAULT '',
    output      TEXT NOT NULL DEFAULT '',
    UNIQUE(message_id, block_index)
);

CREATE TABLE IF NOT EXISTS media (
    upload_id   TEXT PRIMARY KEY,
    message_id  TEXT REFERENCES messages(id) ON DELETE CASCADE,
    conv_id     TEXT NOT NULL,
    file_name   TEXT NOT NULL,
    mime_type   TEXT NOT NULL,
    media_type  TEXT NOT NULL DEFAULT 'document',
    file_path   TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS reactions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL,
    source     TEXT NOT NULL,
    UNIQUE(message_id, emoji, source)
);

CREATE TABLE IF NOT EXISTS summary_covered (
    summary_msg_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    covered_msg_id TEXT NOT NULL,
    PRIMARY KEY (summary_msg_id, covered_msg_id)
);

CREATE TABLE IF NOT EXISTS documents (
    scope       TEXT NOT NULL,
    doc_id      TEXT NOT NULL,
    text        TEXT NOT NULL,
    metadata    TEXT NOT NULL DEFAULT '{}',
    inserted_at INTEGER NOT NULL,
    PRIMARY KEY (scope, doc_id)
);

CREATE TABLE IF NOT EXISTS prefs (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// normalizeBool accepts whatever the SQLite driver returns for a BOOLEAN
// column (int64 0/1, []byte "false"/"true", string "false"/"true", or bool)
// and produces 0 or 1.
func normalizeBool(v any) int {
	switch x := v.(type) {
	case int64:
		if x != 0 {
			return 1
		}
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case []byte:
		s := string(x)
		if s == "true" || s == "1" {
			return 1
		}
		return 0
	case string:
		if x == "true" || x == "1" {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// parseLegacyTime parses a SQLite DATETIME string (as produced by the old
// chatdb CURRENT_TIMESTAMP default) into unix millis. Matches the tolerance
// of the legacy chatdb.parseTime helper that was just deleted.
func parseLegacyTime(s string) int64 {
	if s == "" {
		return 0
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999-07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func migrateConversations(legacyDB *sql.DB, tx *sql.Tx, stats *migrationStats) error {
	rows, err := legacyDB.Query(`SELECT id, title, source, created_at, updated_at, archived FROM conversations`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, title, source, createdAt, updatedAt string
		// BOOLEAN in the legacy schema may be returned by the driver as
		// "false"/"true" depending on SQLite's storage inference; scan as
		// any and normalise.
		var archivedRaw any
		if err := rows.Scan(&id, &title, &source, &createdAt, &updatedAt, &archivedRaw); err != nil {
			return err
		}
		archived := normalizeBool(archivedRaw)
		ct := parseLegacyTime(createdAt)
		ut := parseLegacyTime(updatedAt)
		if ct == 0 {
			ct = time.Now().UnixMilli()
		}
		if ut == 0 {
			ut = ct
		}
		if _, err := tx.Exec(
			`INSERT INTO conversations (id, title, channel, archived, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, title, source, archived, ct, ut,
		); err != nil {
			return fmt.Errorf("insert conv %s: %w", id, err)
		}
		stats.convs++
	}
	return rows.Err()
}

func migrateMessages(legacyDB *sql.DB, tx *sql.Tx, stats *migrationStats) error {
	rows, err := legacyDB.Query(`
		SELECT id, conv_id, seq, role, text, source, model, tier, cost_usd, duration_ms,
		       session_id, reply_to, created_at
		FROM messages ORDER BY conv_id, seq`)
	if err != nil {
		return err
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`
		INSERT INTO messages (id, conv_id, seq, role, channel, content,
		                     model, tier, backend, cost_usd, duration_ms, session_id, reply_to,
		                     tool_call, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			id, convID, role, text, source, model, tier string
			sessionID, replyTo, createdAt                string
			seq, durationMs                              int64
			costUSD                                      float64
		)
		if err := rows.Scan(&id, &convID, &seq, &role, &text, &source, &model, &tier,
			&costUSD, &durationMs, &sessionID, &replyTo, &createdAt); err != nil {
			return err
		}
		ct := parseLegacyTime(createdAt)
		if ct == 0 {
			ct = time.Now().UnixMilli()
		}
		// backend is a new column, defaults to "" — no legacy source.
		if _, err := stmt.Exec(
			id, convID, seq, role, source, text,
			model, tier, "", costUSD, durationMs, sessionID, replyTo,
			"", ct,
		); err != nil {
			return fmt.Errorf("insert msg %s: %w", id, err)
		}
		stats.messages++
	}
	return rows.Err()
}

func migrateContentBlocks(legacyDB *sql.DB, tx *sql.Tx, stats *migrationStats) error {
	rows, err := legacyDB.Query(`
		SELECT message_id, block_index, block_type, text, name, input, tool_id, output
		FROM content_blocks ORDER BY message_id, block_index`)
	if err != nil {
		return err
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`
		INSERT INTO content_blocks (message_id, block_index, block_type, text, name, input, tool_id, output)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			messageID, blockType, text, name, input, toolID, output string
			blockIndex                                              int64
		)
		if err := rows.Scan(&messageID, &blockIndex, &blockType, &text, &name, &input, &toolID, &output); err != nil {
			return err
		}
		if _, err := stmt.Exec(messageID, blockIndex, blockType, text, name, input, toolID, output); err != nil {
			return fmt.Errorf("insert block (%s, %d): %w", messageID, blockIndex, err)
		}
		stats.blocks++
	}
	return rows.Err()
}

func migrateReactions(legacyDB *sql.DB, tx *sql.Tx, stats *migrationStats) error {
	rows, err := legacyDB.Query(`SELECT message_id, emoji, source FROM reactions`)
	if err != nil {
		return err
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO reactions (message_id, emoji, source) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var messageID, emoji, source string
		if err := rows.Scan(&messageID, &emoji, &source); err != nil {
			return err
		}
		if _, err := stmt.Exec(messageID, emoji, source); err != nil {
			return fmt.Errorf("insert reaction (%s, %s): %w", messageID, emoji, err)
		}
		stats.reactions++
	}
	return rows.Err()
}

func migrateMedia(legacyDB *sql.DB, tx *sql.Tx, stats *migrationStats) error {
	rows, err := legacyDB.Query(`
		SELECT upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, url
		FROM media`)
	if err != nil {
		return err
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`
		INSERT INTO media (upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			uploadID, convID, fileName, mimeType, mediaType, filePath, url string
			messageID                                                      sql.NullString
		)
		if err := rows.Scan(&uploadID, &messageID, &convID, &fileName, &mimeType, &mediaType, &filePath, &url); err != nil {
			return err
		}
		var msgIDArg any
		if messageID.Valid {
			msgIDArg = messageID.String
		} else {
			msgIDArg = nil
		}
		if _, err := stmt.Exec(uploadID, msgIDArg, convID, fileName, mimeType, mediaType, filePath, url); err != nil {
			return fmt.Errorf("insert media %s: %w", uploadID, err)
		}
		stats.media++
	}
	return rows.Err()
}

func migrateKVMeta(legacyDB *sql.DB, tx *sql.Tx, stats *migrationStats) error {
	rows, err := legacyDB.Query(`SELECT key, value FROM kv_meta`)
	if err != nil {
		// kv_meta may legitimately not exist on some legacy databases.
		// Legacy schema ALWAYS defined it but very early installs might be odd.
		return nil //nolint:nilerr // treat missing table as no prefs to migrate
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`
		INSERT INTO prefs (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		// memory.Store.SetPref JSON-encodes its values. kv_meta values were
		// plain strings. Wrap as a JSON string so GetPref type-asserts to
		// string cleanly.
		jsonVal, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal kv_meta[%s]: %w", key, err)
		}
		if _, err := stmt.Exec(key, string(jsonVal)); err != nil {
			return fmt.Errorf("insert pref %s: %w", key, err)
		}
		stats.prefs++
	}
	return rows.Err()
}
