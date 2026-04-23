package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alamparelli/alf/internal/memory"

	_ "github.com/mattn/go-sqlite3"
)

// legacyChatDBSchema is the schema the old internal/chatdb package used to
// create. Copied verbatim here so the test can seed a realistic legacy file
// without reviving the deleted package.
const legacyChatDBSchema = `
CREATE TABLE IF NOT EXISTS conversations (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'cc',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived    BOOLEAN NOT NULL DEFAULT 0,
    metadata    TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS messages (
    id          TEXT PRIMARY KEY,
    conv_id     TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL DEFAULT 0,
    role        TEXT NOT NULL,
    text        TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'cc',
    model       TEXT DEFAULT '',
    tier        TEXT DEFAULT '',
    cost_usd    REAL DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    session_id  TEXT DEFAULT '',
    reply_to    TEXT DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS content_blocks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id  TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    block_index INTEGER NOT NULL,
    block_type  TEXT NOT NULL,
    text        TEXT DEFAULT '',
    name        TEXT DEFAULT '',
    input       TEXT DEFAULT '',
    tool_id     TEXT DEFAULT '',
    output      TEXT DEFAULT '',
    UNIQUE(message_id, block_index)
);

CREATE TABLE IF NOT EXISTS reactions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id  TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    emoji       TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT 'user',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(message_id, emoji, source)
);

CREATE TABLE IF NOT EXISTS media (
    upload_id   TEXT PRIMARY KEY,
    message_id  TEXT REFERENCES messages(id) ON DELETE CASCADE,
    conv_id     TEXT NOT NULL,
    file_name   TEXT NOT NULL,
    mime_type   TEXT NOT NULL,
    media_type  TEXT NOT NULL DEFAULT 'document',
    file_path   TEXT DEFAULT '',
    url         TEXT DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS kv_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
`

// seedLegacyChatDB writes a realistic chat.db under dataDir/logs/chat.db with
// a representative mix of rows across every table. Returns the path.
func seedLegacyChatDB(t *testing.T, dataDir string) string {
	t.Helper()
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dbPath := filepath.Join(logsDir, "chat.db")
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=1")
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyChatDBSchema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	// Two conversations: cc + telegram.
	_, _ = db.Exec(`INSERT INTO conversations (id, title, source, created_at, updated_at, archived) VALUES
		('cc-1', 'greeting', 'cc', '2026-04-01 10:00:00', '2026-04-01 10:05:00', 0),
		('tg-1', 'bot',      'tg', '2026-04-01 11:00:00', '2026-04-01 11:02:00', 0)`)

	// Two messages in cc-1, one in tg-1. Include reply_to, model/tier/cost.
	_, _ = db.Exec(`INSERT INTO messages (id, conv_id, seq, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at) VALUES
		('m-cc-1', 'cc-1', 1, 'user',      'hello',        'cc', '',              '',    0.0,   0,   '',       '',       '2026-04-01 10:00:30'),
		('m-cc-2', 'cc-1', 2, 'assistant', 'hi there',     'cc', 'claude-opus-4', 'hero', 0.02, 520, 'sess-1', 'm-cc-1', '2026-04-01 10:00:32'),
		('m-tg-1', 'tg-1', 1, 'user',      'bot you up?',  'tg', '',              '',    0.0,   0,   '',       '',       '2026-04-01 11:00:10')`)

	// Content blocks on m-cc-2: text + tool_use + tool_result.
	_, _ = db.Exec(`INSERT INTO content_blocks (message_id, block_index, block_type, text, name, input, tool_id, output) VALUES
		('m-cc-2', 0, 'text',        'hi there',       '',        '',             '',   ''),
		('m-cc-2', 1, 'tool_use',    '',               'read_file','{"path":"x"}', 't1', ''),
		('m-cc-2', 2, 'tool_result', '',               '',        '',             't1', 'file contents')`)

	// Reactions on m-cc-2.
	_, _ = db.Exec(`INSERT INTO reactions (message_id, emoji, source) VALUES
		('m-cc-2', '👍', 'user'),
		('m-cc-2', '🙏', 'alf')`)

	// One media ref attached to m-cc-1.
	_, _ = db.Exec(`INSERT INTO media (upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, url) VALUES
		('upl-1', 'm-cc-1', 'cc-1', 'cat.png', 'image/png', 'photo', '/tmp/cat.png', '')`)

	// Legacy kv_meta: the "active conv id" preference.
	_, _ = db.Exec(`INSERT INTO kv_meta (key, value) VALUES ('active_conv_id', 'cc-1')`)

	return dbPath
}

func TestMigrateChatDBToMemoryDB_RoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	legacyPath := seedLegacyChatDB(t, dataDir)

	if err := migrateChatDBToMemoryDB(dataDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Sentinel: chat.db renamed to chat.db.migrated.
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("expected chat.db to be renamed, still present: %v", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Errorf("expected chat.db.migrated sentinel: %v", err)
	}

	// Verify memory.db contents.
	mem, err := sql.Open("sqlite3", filepath.Join(dataDir, "memory.db"))
	if err != nil {
		t.Fatalf("open memory.db: %v", err)
	}
	defer mem.Close()

	// Conversations.
	var ccChannel, tgChannel string
	if err := mem.QueryRow(`SELECT channel FROM conversations WHERE id = 'cc-1'`).Scan(&ccChannel); err != nil {
		t.Fatalf("cc-1 missing: %v", err)
	}
	if ccChannel != "cc" {
		t.Errorf("cc-1 channel = %q, want cc", ccChannel)
	}
	if err := mem.QueryRow(`SELECT channel FROM conversations WHERE id = 'tg-1'`).Scan(&tgChannel); err != nil {
		t.Fatalf("tg-1 missing: %v", err)
	}
	if tgChannel != "tg" {
		t.Errorf("tg-1 channel = %q, want tg", tgChannel)
	}

	// created_at is now int64 millis, should be non-zero post-conversion.
	var ccCreated int64
	_ = mem.QueryRow(`SELECT created_at FROM conversations WHERE id = 'cc-1'`).Scan(&ccCreated)
	if ccCreated == 0 {
		t.Error("cc-1 created_at not converted")
	}

	// Messages: count + field mapping (text → content, source → channel).
	var msgCount int
	_ = mem.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	if msgCount != 3 {
		t.Errorf("want 3 msgs, got %d", msgCount)
	}
	var content, channel, replyTo string
	var costUSD float64
	_ = mem.QueryRow(`SELECT content, channel, reply_to, cost_usd FROM messages WHERE id = 'm-cc-2'`).
		Scan(&content, &channel, &replyTo, &costUSD)
	if content != "hi there" {
		t.Errorf("m-cc-2 content = %q, want 'hi there'", content)
	}
	if channel != "cc" {
		t.Errorf("m-cc-2 channel = %q, want cc", channel)
	}
	if replyTo != "m-cc-1" {
		t.Errorf("m-cc-2 reply_to = %q, want m-cc-1", replyTo)
	}
	if costUSD != 0.02 {
		t.Errorf("m-cc-2 cost_usd = %v, want 0.02", costUSD)
	}

	// Per-conv seq preserved.
	var cc2Seq int64
	_ = mem.QueryRow(`SELECT seq FROM messages WHERE id = 'm-cc-2'`).Scan(&cc2Seq)
	if cc2Seq != 2 {
		t.Errorf("m-cc-2 seq = %d, want 2", cc2Seq)
	}

	// Content blocks.
	var blockCount int
	_ = mem.QueryRow(`SELECT COUNT(*) FROM content_blocks WHERE message_id = 'm-cc-2'`).Scan(&blockCount)
	if blockCount != 3 {
		t.Errorf("want 3 blocks for m-cc-2, got %d", blockCount)
	}
	var toolUseName, toolUseInput string
	_ = mem.QueryRow(`SELECT name, input FROM content_blocks WHERE message_id = 'm-cc-2' AND block_index = 1`).
		Scan(&toolUseName, &toolUseInput)
	if toolUseName != "read_file" || toolUseInput != `{"path":"x"}` {
		t.Errorf("tool_use block mangled: name=%q input=%q", toolUseName, toolUseInput)
	}

	// Reactions.
	var reactCount int
	_ = mem.QueryRow(`SELECT COUNT(*) FROM reactions WHERE message_id = 'm-cc-2'`).Scan(&reactCount)
	if reactCount != 2 {
		t.Errorf("want 2 reactions, got %d", reactCount)
	}

	// Media.
	var mediaFile string
	_ = mem.QueryRow(`SELECT file_name FROM media WHERE upload_id = 'upl-1'`).Scan(&mediaFile)
	if mediaFile != "cat.png" {
		t.Errorf("media file_name = %q, want cat.png", mediaFile)
	}

	// Prefs (kv_meta → prefs, JSON-encoded).
	var activeConvRaw string
	if err := mem.QueryRow(`SELECT value FROM prefs WHERE key = 'active_conv_id'`).Scan(&activeConvRaw); err != nil {
		t.Fatalf("active_conv_id pref missing: %v", err)
	}
	if activeConvRaw != `"cc-1"` {
		t.Errorf("pref stored as %q, want JSON-wrapped \"cc-1\"", activeConvRaw)
	}
}

func TestMigrateChatDBToMemoryDB_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	seedLegacyChatDB(t, dataDir)

	// First run migrates.
	if err := migrateChatDBToMemoryDB(dataDir); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run is a no-op (sentinel exists).
	if err := migrateChatDBToMemoryDB(dataDir); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	// memory.db still has 3 msgs (no duplicates).
	mem, _ := sql.Open("sqlite3", filepath.Join(dataDir, "memory.db"))
	defer mem.Close()
	var n int
	_ = mem.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	if n != 3 {
		t.Errorf("second run duplicated rows: got %d messages", n)
	}
}

func TestMigrateChatDBToMemoryDB_FreshInstall(t *testing.T) {
	dataDir := t.TempDir()
	// No chat.db present — must be a no-op, not an error.
	if err := migrateChatDBToMemoryDB(dataDir); err != nil {
		t.Fatalf("fresh-install migrate: %v", err)
	}
	// memory.db not created yet (we only create it when there's data to migrate).
	if _, err := os.Stat(filepath.Join(dataDir, "memory.db")); !os.IsNotExist(err) {
		t.Errorf("memory.db should not exist on fresh-install path: %v", err)
	}
}

// TestMigrateChatDBToMemoryDB_StoreAPIRoundTrip is a smoke test of the
// post-migration path a real daemon boot would follow: after the raw SQL
// migration, open memory.db via memory.NewSQLiteStore (the production API)
// and verify every caller-visible entry point returns the migrated rows.
// If this passes, a daemon boot against a dataDir with a legacy chat.db is
// safe — consumers reading through memory.Store see their full history.
func TestMigrateChatDBToMemoryDB_StoreAPIRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	seedLegacyChatDB(t, dataDir)

	if err := migrateChatDBToMemoryDB(dataDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore after migration: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()

	// Conv listing: 2 convs, channels preserved.
	convs, err := store.ListConvs(ctx, memory.ConvFilter{})
	if err != nil {
		t.Fatalf("ListConvs: %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("want 2 convs, got %d", len(convs))
	}
	byID := map[memory.ConvID]memory.ConvInfo{}
	for _, c := range convs {
		byID[c.ID] = c
	}
	if byID["cc-1"].Channel != "cc" || byID["tg-1"].Channel != "tg" {
		t.Errorf("channel mapping broken: %+v", byID)
	}
	if byID["cc-1"].MsgCount != 2 {
		t.Errorf("cc-1 MsgCount = %d, want 2", byID["cc-1"].MsgCount)
	}

	// ListMessages on cc-1 returns both in seq order with rich blocks.
	msgs, err := store.ListMessages(ctx, "cc-1", memory.ListOpts{})
	if err != nil {
		t.Fatalf("ListMessages(cc-1): %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 msgs in cc-1, got %d", len(msgs))
	}
	if msgs[0].ID != "m-cc-1" || msgs[1].ID != "m-cc-2" {
		t.Errorf("seq order broken: %v / %v", msgs[0].ID, msgs[1].ID)
	}
	if msgs[0].Content != "hello" || msgs[1].Content != "hi there" {
		t.Errorf("content mapping broken: %q / %q", msgs[0].Content, msgs[1].Content)
	}
	// m-cc-2 carries 3 blocks + 2 reactions.
	if len(msgs[1].Blocks) != 3 {
		t.Errorf("m-cc-2 blocks = %d, want 3", len(msgs[1].Blocks))
	}
	if len(msgs[1].Reactions) != 2 {
		t.Errorf("m-cc-2 reactions = %d, want 2", len(msgs[1].Reactions))
	}
	// m-cc-1 carries 1 media.
	if len(msgs[0].Media) != 1 || msgs[0].Media[0].UploadID != "upl-1" {
		t.Errorf("m-cc-1 media malformed: %+v", msgs[0].Media)
	}

	// GetMessage scoped to conv returns the exact message.
	got, err := store.GetMessage(ctx, "cc-1", "m-cc-2")
	if err != nil || got == nil {
		t.Fatalf("GetMessage m-cc-2: err=%v got=%+v", err, got)
	}
	if got.Tier != "hero" || got.Model != "claude-opus-4" || got.CostUSD != 0.02 {
		t.Errorf("bookkeeping fields lost: tier=%q model=%q cost=%v", got.Tier, got.Model, got.CostUSD)
	}
	if got.ReplyTo != "m-cc-1" {
		t.Errorf("reply_to lost: %q", got.ReplyTo)
	}

	// LatestConvID for "cc" should be cc-1 (only non-archived cc conv with messages).
	latest, err := store.LatestConvID(ctx, "cc")
	if err != nil {
		t.Fatalf("LatestConvID: %v", err)
	}
	if latest != "cc-1" {
		t.Errorf("LatestConvID = %q, want cc-1", latest)
	}

	// Prefs: kv_meta.active_conv_id round-trips as a string through GetPref.
	v, err := store.GetPref(ctx, "active_conv_id")
	if err != nil {
		t.Fatalf("GetPref: %v", err)
	}
	s, ok := v.(string)
	if !ok || s != "cc-1" {
		t.Errorf("pref active_conv_id: got %v (type %T), want string \"cc-1\"", v, v)
	}

	// A fresh write on top of migrated data must not collide on IDs or seq.
	if _, err := store.AppendMessage(ctx, "cc-1", memory.Message{Role: "user", Content: "new message"}); err != nil {
		t.Fatalf("AppendMessage after migration: %v", err)
	}
	msgs, _ = store.ListMessages(ctx, "cc-1", memory.ListOpts{})
	if len(msgs) != 3 {
		t.Fatalf("after append, want 3 msgs, got %d", len(msgs))
	}
	if msgs[2].Seq != 3 {
		t.Errorf("new msg seq = %d, want 3 (following legacy seq 1, 2)", msgs[2].Seq)
	}
}

func TestMigrateChatDBToMemoryDB_RefusesIfMemoryDBNonEmpty(t *testing.T) {
	dataDir := t.TempDir()
	seedLegacyChatDB(t, dataDir)

	// Pre-populate memory.db with an unrelated message.
	memPath := filepath.Join(dataDir, "memory.db")
	mem, _ := sql.Open("sqlite3", memPath+"?_foreign_keys=1")
	defer mem.Close()
	_, _ = mem.Exec(memoryDBSchema)
	_, _ = mem.Exec(`INSERT INTO conversations (id, created_at, updated_at) VALUES ('pre', 1, 1)`)
	_, _ = mem.Exec(`INSERT INTO messages (id, conv_id, seq, role, created_at) VALUES ('pre-m', 'pre', 1, 'user', 1)`)

	err := migrateChatDBToMemoryDB(dataDir)
	if err == nil {
		t.Fatal("expected refusal when memory.db is non-empty")
	}
	// Sentinel MUST NOT be created — the operator needs to be able to retry.
	if _, err := os.Stat(filepath.Join(dataDir, "logs", "chat.db.migrated")); !os.IsNotExist(err) {
		t.Errorf("sentinel created despite refusal — operator cannot retry")
	}
}
