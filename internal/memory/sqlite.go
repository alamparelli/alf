package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	// Register the sqlite-vec extension on every sqlite3 connection opened
	// after this point. Safe to call from multiple packages — it's
	// idempotent in sqlite_vec/cgo.
	sqlite_vec.Auto()
}

// SQLiteStore is the production Store backend for Step 1.2 (#336). It
// absorbs the old chatdb + conversation packages under one SQLite database
// (typically data-dir/memory.db) and passes the memtest.RunStoreContract
// harness unchanged.
//
// Concurrency: writer serialization is done by pinning the sql.DB pool to
// one connection (SetMaxOpenConns(1)). Same reasoning as the old chatdb
// (see #346): SQLite permits one writer at a time and the DSN busy_timeout
// pragma is not re-applied to pool-borrowed connections, so relying on a
// larger pool leads to "database is locked" races.
type SQLiteStore struct {
	db     *sql.DB
	msgSeq atomic.Uint64

	// Optional embedding pipeline. When nil, Index/Search use the legacy
	// LIKE substring path; when set, Index also stores a float[dim] vector
	// in the documents_vec virtual table and Search uses cosine similarity.
	embedder Embedder
	embedDim int // cached Dims() at open time; 0 means vec disabled

	// nowFn is the injectable clock. Defaults to time.Now().UnixMilli.
	// Exposed only for tests in the same package.
	nowFn func() int64

	closeOnce sync.Once
}

// StoreOption configures a SQLiteStore at construction time.
//
// Options are applied in order and MUST only touch fields of the store —
// the schema is materialised after options run so an option may opt into
// features (e.g. vector search) that depend on extra tables.
type StoreOption func(*SQLiteStore)

// WithEmbedder enables semantic Index/Search backed by sqlite-vec. The
// embedder's Dims() must be non-zero; passing an embedder with Dims() == 0
// is treated as "no embedder" and the store falls back to the LIKE path.
//
// Swapping embedder implementations between runs is allowed as long as
// Dims() stays the same — the vec0 virtual table is created with a fixed
// dimension and SQLite does not let you alter that in place.
func WithEmbedder(e Embedder) StoreOption {
	return func(s *SQLiteStore) {
		if e == nil || e.Dims() <= 0 {
			return
		}
		s.embedder = e
		s.embedDim = e.Dims()
	}
}

const sqliteSchema = `
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

// NewSQLiteStore opens or creates memory.db in dataDir. The file is created
// if missing; the schema is created idempotently.
//
// Pass an empty dataDir to use an in-memory SQLite DB (":memory:") — intended
// for tests only. Production callers always pass a real directory.
//
// Options configure optional subsystems. WithEmbedder enables semantic
// search — the vec0 / FTS5 tables are materialised only when an embedder
// is present so the default-path schema stays CGO-vec-free for callers
// that don't need it.
func NewSQLiteStore(dataDir string, opts ...StoreOption) (*SQLiteStore, error) {
	var dbPath string
	if dataDir == "" {
		dbPath = ":memory:"
	} else {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("memory: MkdirAll: %w", err)
		}
		dbPath = filepath.Join(dataDir, "memory.db")
	}

	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: sql.Open: %w", err)
	}
	// Single-writer: SQLite's busy_timeout is per-connection and Go's pool
	// does not re-apply it. Same fix as chatdb (#346).
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: PRAGMA foreign_keys: %w", err)
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: schema: %w", err)
	}

	s := &SQLiteStore{
		db:    db,
		nowFn: func() int64 { return time.Now().UnixMilli() },
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.embedDim > 0 {
		if err := s.migrateVecSchema(); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: vec schema: %w", err)
		}
	}

	// Seed the in-process sequence from the highest existing message ID so
	// reopening a DB does not collide with old IDs.
	var maxSeq sql.NullInt64
	_ = db.QueryRow(
		`SELECT COALESCE(MAX(CAST(substr(id, 2) AS INTEGER)), 0) FROM messages`,
	).Scan(&maxSeq)
	if maxSeq.Valid && maxSeq.Int64 > 0 {
		s.msgSeq.Store(uint64(maxSeq.Int64))
	}

	return s, nil
}

// migrateVecSchema adds the vec0 + FTS5 tables used for semantic Search.
// Called only when an embedder is configured; the default schema stays
// vec-free so callers that don't need semantic search pay no CGO-vec cost
// at open time.
//
// The documents table owns an INTEGER rowid (implicit in SQLite); we use
// that rowid as the join key for both virtual tables. The vec dimension is
// baked into the DDL — swapping embedders with a different dim requires
// dropping and rebuilding the index (handled in a later sub-ticket).
func (s *SQLiteStore) migrateVecSchema() error {
	// Idempotency: record the dim the index was created with and refuse to
	// reopen with a different one. Matches the memstore invariant — vec0
	// cannot change its declared dimension in place.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS doc_vec_meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("doc_vec_meta: %w", err)
	}
	var existingDim string
	_ = s.db.QueryRow(`SELECT value FROM doc_vec_meta WHERE key = 'dim'`).Scan(&existingDim)
	if existingDim != "" && existingDim != fmt.Sprintf("%d", s.embedDim) {
		return fmt.Errorf("memory: vec dim mismatch: db was built with %s, embedder reports %d", existingDim, s.embedDim)
	}
	if existingDim == "" {
		if _, err := s.db.Exec(`INSERT INTO doc_vec_meta (key, value) VALUES ('dim', ?)`, fmt.Sprintf("%d", s.embedDim)); err != nil {
			return fmt.Errorf("doc_vec_meta insert: %w", err)
		}
	}

	stmts := []string{
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS documents_vec USING vec0(
			rowid INTEGER PRIMARY KEY,
			embedding float[%d] distance_metric=cosine
		)`, s.embedDim),
		`CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
			text, scope UNINDEXED, content=documents, content_rowid=rowid
		)`,
		// Keep FTS5 mirror in sync with documents. vec0 is written
		// explicitly by Index() — we don't trigger it from SQL because the
		// embedding must be computed in Go.
		`CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, text, scope) VALUES (new.rowid, new.text, new.scope);
		END`,
		`CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, text, scope) VALUES('delete', old.rowid, old.text, old.scope);
		END`,
		`CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, text, scope) VALUES('delete', old.rowid, old.text, old.scope);
			INSERT INTO documents_fts(rowid, text, scope) VALUES (new.rowid, new.text, new.scope);
		END`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			head := stmt
			if len(head) > 80 {
				head = head[:80]
			}
			return fmt.Errorf("exec %q: %w", head, err)
		}
	}
	return nil
}

// serializeVec converts a float32 vector into the little-endian byte blob
// that sqlite-vec expects for a vec0 column.
func serializeVec(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// Close releases the database handle. Safe to call multiple times.
func (s *SQLiteStore) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.db != nil {
			err = s.db.Close()
		}
	})
	return err
}

func (s *SQLiteStore) newMsgID() MsgID {
	n := s.msgSeq.Add(1)
	return MsgID(fmt.Sprintf("m%d", n))
}

// Conversations ---------------------------------------------------------------

func (s *SQLiteStore) EnsureConv(ctx context.Context, convID ConvID, title string, channel Channel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: EnsureConv: empty convID")
	}
	now := s.nowFn()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (id, title, channel, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		string(convID), title, channel, now, now)
	return err
}

func (s *SQLiteStore) GetConv(ctx context.Context, convID ConvID) (ConvInfo, error) {
	if err := ctx.Err(); err != nil {
		return ConvInfo{}, err
	}
	if convID == "" {
		return ConvInfo{}, errors.New("memory: GetConv: empty convID")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.title, c.channel, c.archived, c.created_at, c.updated_at,
		       COALESCE(s.cnt, 0), COALESCE(s.last_msg, 0)
		FROM conversations c
		LEFT JOIN (
		    SELECT conv_id, COUNT(*) AS cnt, MAX(created_at) AS last_msg
		    FROM messages GROUP BY conv_id
		) s ON s.conv_id = c.id
		WHERE c.id = ?`, string(convID))

	var info ConvInfo
	var archived int
	if err := row.Scan(&info.ID, &info.Title, &info.Channel, &archived, &info.CreatedAt, &info.UpdatedAt, &info.MsgCount, &info.LastMessage); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ConvInfo{}, nil
		}
		return ConvInfo{}, err
	}
	info.Archived = archived == 1
	return info, nil
}

func (s *SQLiteStore) ListConvs(ctx context.Context, filter ConvFilter) ([]ConvInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q := `
		SELECT c.id, c.title, c.channel, c.archived, c.created_at, c.updated_at,
		       COALESCE(s.cnt, 0), COALESCE(s.last_msg, 0)
		FROM conversations c
		LEFT JOIN (
		    SELECT conv_id, COUNT(*) AS cnt, MAX(created_at) AS last_msg
		    FROM messages GROUP BY conv_id
		) s ON s.conv_id = c.id`

	var where []string
	var args []any
	if filter.Channel != "" {
		where = append(where, "c.channel = ?")
		args = append(args, string(filter.Channel))
	}
	if !filter.IncludeArchived {
		where = append(where, "c.archived = 0")
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY c.created_at ASC, c.id ASC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConvInfo
	for rows.Next() {
		var info ConvInfo
		var archived int
		if err := rows.Scan(&info.ID, &info.Title, &info.Channel, &archived, &info.CreatedAt, &info.UpdatedAt, &info.MsgCount, &info.LastMessage); err != nil {
			return nil, err
		}
		info.Archived = archived == 1
		out = append(out, info)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateConvTitle(ctx context.Context, convID ConvID, title string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: UpdateConvTitle: empty convID")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?`,
		title, s.nowFn(), string(convID))
	return err
}

func (s *SQLiteStore) ArchiveConv(ctx context.Context, convID ConvID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: ArchiveConv: empty convID")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET archived = 1, updated_at = ? WHERE id = ?`,
		s.nowFn(), string(convID))
	return err
}

func (s *SQLiteStore) DeleteConv(ctx context.Context, convID ConvID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: DeleteConv: empty convID")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM conversations WHERE id = ?`, string(convID))
	return err
}

func (s *SQLiteStore) LatestConvID(ctx context.Context, channel Channel) (ConvID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// rowid on messages is monotonic per-insert, so MAX(m.rowid) gives us
	// the correct last-write ordering even when timestamps collide.
	q := `
		SELECT c.id
		FROM conversations c
		JOIN messages m ON m.conv_id = c.id
		WHERE c.archived = 0`
	var args []any
	if channel != "" {
		q += " AND c.channel = ?"
		args = append(args, string(channel))
	}
	q += " ORDER BY m.rowid DESC LIMIT 1"

	var id string
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return ConvID(id), nil
}

// Messages --------------------------------------------------------------------

func (s *SQLiteStore) AppendMessage(ctx context.Context, convID ConvID, msg Message) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if convID == "" {
		return Message{}, errors.New("memory: AppendMessage: empty convID")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	now := s.nowFn()
	// Auto-create conversation if missing (mirrors the old chatdb behaviour
	// where InsertMessage was tolerant of absent conv rows via FK OR the
	// caller's EnsureConversation — we keep the silent-create path so
	// direct AppendMessage on a fresh conv still works).
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (id, title, channel, created_at, updated_at)
		 VALUES (?, '', '', ?, ?)`,
		string(convID), now, now); err != nil {
		return Message{}, fmt.Errorf("memory: AppendMessage: ensure conv: %w", err)
	}

	// Next per-conv seq.
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE conv_id = ?`,
		string(convID)).Scan(&seq); err != nil {
		return Message{}, fmt.Errorf("memory: AppendMessage: next seq: %w", err)
	}

	msg.ID = s.newMsgID()
	msg.Seq = seq
	msg.CreatedAt = now

	var toolCallJSON string
	if msg.ToolCall != nil {
		b, err := json.Marshal(msg.ToolCall)
		if err != nil {
			return Message{}, fmt.Errorf("memory: AppendMessage: marshal tool_call: %w", err)
		}
		toolCallJSON = string(b)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
		    id, conv_id, seq, role, channel, content,
		    model, tier, backend, cost_usd, duration_ms, session_id, reply_to,
		    tool_call, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(msg.ID), string(convID), msg.Seq, msg.Role, msg.Channel, msg.Content,
		msg.Model, msg.Tier, msg.Backend, msg.CostUSD, msg.DurationMs, msg.SessionID, string(msg.ReplyTo),
		toolCallJSON, msg.CreatedAt,
	); err != nil {
		return Message{}, fmt.Errorf("memory: AppendMessage: insert message: %w", err)
	}

	for i, b := range msg.Blocks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO content_blocks (message_id, block_index, block_type, text, name, input, tool_id, output)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			string(msg.ID), i, string(b.Type), b.Text, b.Name, b.Input, b.ToolID, b.Output,
		); err != nil {
			return Message{}, fmt.Errorf("memory: AppendMessage: insert block %d: %w", i, err)
		}
	}

	for _, m := range msg.Media {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO media (upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, url)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.UploadID, string(msg.ID), string(convID), m.FileName, m.MimeType, m.MediaType, m.FilePath, m.URL,
		); err != nil {
			return Message{}, fmt.Errorf("memory: AppendMessage: insert media %s: %w", m.UploadID, err)
		}
	}

	for _, r := range msg.Reactions {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO reactions (message_id, emoji, source) VALUES (?, ?, ?)`,
			string(msg.ID), r.Emoji, r.Source,
		); err != nil {
			return Message{}, fmt.Errorf("memory: AppendMessage: insert reaction: %w", err)
		}
	}

	for _, cid := range msg.CoveredIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO summary_covered (summary_msg_id, covered_msg_id) VALUES (?, ?)`,
			string(msg.ID), string(cid),
		); err != nil {
			return Message{}, fmt.Errorf("memory: AppendMessage: insert summary_covered: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE conversations SET updated_at = ? WHERE id = ?`,
		now, string(convID),
	); err != nil {
		return Message{}, fmt.Errorf("memory: AppendMessage: bump updated_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (s *SQLiteStore) GetMessage(ctx context.Context, convID ConvID, msgID MsgID) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if convID == "" {
		return nil, errors.New("memory: GetMessage: empty convID")
	}
	if msgID == "" {
		return nil, errors.New("memory: GetMessage: empty msgID")
	}
	msgs, err := s.loadMessages(ctx, `WHERE m.conv_id = ? AND m.id = ?`, string(convID), string(msgID))
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	cp := msgs[0]
	return &cp, nil
}

func (s *SQLiteStore) ListMessages(ctx context.Context, convID ConvID, opts ListOpts) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if convID == "" {
		return nil, errors.New("memory: ListMessages: empty convID")
	}

	var (
		seqAfter  int64
		seqBefore int64 = -1
	)
	if opts.After != "" {
		if err := s.db.QueryRowContext(ctx,
			`SELECT seq FROM messages WHERE conv_id = ? AND id = ?`,
			string(convID), string(opts.After),
		).Scan(&seqAfter); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if opts.Before != "" {
		if err := s.db.QueryRowContext(ctx,
			`SELECT seq FROM messages WHERE conv_id = ? AND id = ?`,
			string(convID), string(opts.Before),
		).Scan(&seqBefore); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	args := []any{string(convID)}
	conds := []string{"m.conv_id = ?"}
	if opts.After != "" && seqAfter > 0 {
		conds = append(conds, "m.seq > ?")
		args = append(args, seqAfter)
	}
	if opts.Before != "" && seqBefore > 0 {
		conds = append(conds, "m.seq < ?")
		args = append(args, seqBefore)
	}
	whereSQL := "WHERE " + strings.Join(conds, " AND ")

	msgs, err := s.loadMessages(ctx, whereSQL, args...)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	if opts.ApplySummary {
		msgs = applySummaryInPlace(msgs)
	}
	if opts.Limit > 0 && len(msgs) > opts.Limit {
		msgs = msgs[len(msgs)-opts.Limit:]
	}
	return msgs, nil
}

// loadMessages runs a SELECT against messages joined with its child tables,
// then post-assembles Blocks/Media/Reactions/CoveredIDs per message. The
// whereSQL argument must start with "WHERE" and use '?' placeholders; args
// are passed through unchanged.
func (s *SQLiteStore) loadMessages(ctx context.Context, whereSQL string, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.seq, m.role, m.channel, m.content,
		       m.model, m.tier, m.backend, m.cost_usd, m.duration_ms,
		       m.session_id, m.reply_to, m.tool_call, m.created_at
		FROM messages m
		`+whereSQL+`
		ORDER BY m.conv_id, m.seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	var ids []string
	for rows.Next() {
		var m Message
		var id, replyTo, toolCall string
		if err := rows.Scan(
			&id, &m.Seq, &m.Role, &m.Channel, &m.Content,
			&m.Model, &m.Tier, &m.Backend, &m.CostUSD, &m.DurationMs,
			&m.SessionID, &replyTo, &toolCall, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		m.ID = MsgID(id)
		m.ReplyTo = MsgID(replyTo)
		if toolCall != "" {
			var tc ToolCall
			if err := json.Unmarshal([]byte(toolCall), &tc); err == nil {
				m.ToolCall = &tc
			}
		}
		out = append(out, m)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}

	// Load children in bulk.
	blocks, err := s.loadBlocks(ctx, ids)
	if err != nil {
		return nil, err
	}
	media, err := s.loadMedia(ctx, ids)
	if err != nil {
		return nil, err
	}
	reacts, err := s.loadReactions(ctx, ids)
	if err != nil {
		return nil, err
	}
	covered, err := s.loadCovered(ctx, ids)
	if err != nil {
		return nil, err
	}

	for i := range out {
		id := ids[i]
		out[i].Blocks = blocks[id]
		out[i].Media = media[id]
		out[i].Reactions = reacts[id]
		out[i].CoveredIDs = covered[id]
	}
	return out, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return "?" + strings.Repeat(",?", n-1)
}

func (s *SQLiteStore) loadBlocks(ctx context.Context, ids []string) (map[string][]ContentBlock, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT message_id, block_type, text, name, input, tool_id, output
	      FROM content_blocks WHERE message_id IN (` + placeholders(len(ids)) + `)
	      ORDER BY message_id, block_index`
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]ContentBlock)
	for rows.Next() {
		var mid string
		var b ContentBlock
		var bt string
		if err := rows.Scan(&mid, &bt, &b.Text, &b.Name, &b.Input, &b.ToolID, &b.Output); err != nil {
			return nil, err
		}
		b.Type = BlockType(bt)
		out[mid] = append(out[mid], b)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) loadMedia(ctx context.Context, ids []string) (map[string][]Media, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT message_id, upload_id, file_name, mime_type, media_type, file_path, url
	      FROM media WHERE message_id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]Media)
	for rows.Next() {
		var mid sql.NullString
		var m Media
		if err := rows.Scan(&mid, &m.UploadID, &m.FileName, &m.MimeType, &m.MediaType, &m.FilePath, &m.URL); err != nil {
			return nil, err
		}
		if !mid.Valid {
			continue
		}
		out[mid.String] = append(out[mid.String], m)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) loadReactions(ctx context.Context, ids []string) (map[string][]Reaction, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT message_id, emoji, source FROM reactions
	      WHERE message_id IN (` + placeholders(len(ids)) + `) ORDER BY id`
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]Reaction)
	for rows.Next() {
		var mid string
		var r Reaction
		if err := rows.Scan(&mid, &r.Emoji, &r.Source); err != nil {
			return nil, err
		}
		out[mid] = append(out[mid], r)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) loadCovered(ctx context.Context, ids []string) (map[string][]MsgID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT summary_msg_id, covered_msg_id FROM summary_covered
	      WHERE summary_msg_id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, len(ids))
	for i, v := range ids {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]MsgID)
	for rows.Next() {
		var sid, cid string
		if err := rows.Scan(&sid, &cid); err != nil {
			return nil, err
		}
		out[sid] = append(out[sid], MsgID(cid))
	}
	return out, rows.Err()
}

func (s *SQLiteStore) AddReaction(ctx context.Context, convID ConvID, msgID MsgID, r Reaction) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if convID == "" {
		return false, errors.New("memory: AddReaction: empty convID")
	}
	if msgID == "" {
		return false, errors.New("memory: AddReaction: empty msgID")
	}

	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM messages WHERE conv_id = ? AND id = ?`,
		string(convID), string(msgID),
	).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO reactions (message_id, emoji, source) VALUES (?, ?, ?)`,
		string(msgID), r.Emoji, r.Source,
	); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) AppendSummary(ctx context.Context, convID ConvID, text string, coveredIDs []MsgID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: AppendSummary: empty convID")
	}
	if text == "" || len(coveredIDs) == 0 {
		return nil
	}
	_, err := s.AppendMessage(ctx, convID, Message{
		Role:       RoleSummary,
		Content:    text,
		Blocks:     []ContentBlock{{Type: BlockSummary, Text: text}},
		CoveredIDs: coveredIDs,
	})
	return err
}

func (s *SQLiteStore) LatestSummaryCovered(ctx context.Context, convID ConvID) ([]MsgID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if convID == "" {
		return nil, errors.New("memory: LatestSummaryCovered: empty convID")
	}

	var summaryID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM messages
		 WHERE conv_id = ? AND role = ?
		 ORDER BY seq DESC LIMIT 1`,
		string(convID), RoleSummary,
	).Scan(&summaryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT covered_msg_id FROM summary_covered WHERE summary_msg_id = ?`,
		summaryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MsgID
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, err
		}
		out = append(out, MsgID(cid))
	}
	return out, rows.Err()
}

func (s *SQLiteStore) Summarize(ctx context.Context, convID ConvID) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if convID == "" {
		return Summary{}, errors.New("memory: Summarize: empty convID")
	}

	// If a summary message already exists, return it.
	var sumID, sumText string
	var createdAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, content, created_at FROM messages
		 WHERE conv_id = ? AND role = ?
		 ORDER BY seq DESC LIMIT 1`,
		string(convID), RoleSummary,
	).Scan(&sumID, &sumText, &createdAt)
	if err == nil {
		return Summary{
			ConvID:    convID,
			Text:      sumText,
			UpToMsgID: MsgID(sumID),
			CreatedAt: createdAt,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Summary{}, err
	}

	// No summary yet — build a deterministic stub.
	var count int64
	var lastID string
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(id), '') FROM messages WHERE conv_id = ?`,
		string(convID))
	if err := row.Scan(&count, &lastID); err != nil {
		return Summary{}, err
	}
	if count == 0 {
		return Summary{}, nil
	}
	return Summary{
		ConvID:    convID,
		Text:      fmt.Sprintf("conv %s: %d message(s)", convID, count),
		UpToMsgID: MsgID(lastID),
		CreatedAt: s.nowFn(),
	}, nil
}

// Embeddings -----------------------------------------------------------------

func (s *SQLiteStore) Index(ctx context.Context, scope Scope, doc Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope == "" {
		return errors.New("memory: Index: empty scope")
	}
	if doc.ID == "" {
		return errors.New("memory: Index: empty Document.ID")
	}
	meta := "{}"
	if len(doc.Metadata) > 0 {
		b, err := json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("memory: Index: marshal metadata: %w", err)
		}
		meta = string(b)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO documents (scope, doc_id, text, metadata, inserted_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(scope, doc_id) DO UPDATE SET
		    text = excluded.text,
		    metadata = excluded.metadata,
		    inserted_at = excluded.inserted_at`,
		string(scope), doc.ID, doc.Text, meta, s.nowFn())
	if err != nil {
		return err
	}

	if s.embedder == nil || !s.embedder.IsReady() {
		return nil
	}

	// Resolve the document's rowid — the INSERT…ON CONFLICT path returns
	// LastInsertId only on the insert branch, so for upserts we re-query.
	rowID, lidErr := res.LastInsertId()
	if lidErr != nil || rowID == 0 {
		if err := s.db.QueryRowContext(ctx,
			`SELECT rowid FROM documents WHERE scope = ? AND doc_id = ?`,
			string(scope), doc.ID).Scan(&rowID); err != nil {
			return fmt.Errorf("memory: Index: rowid lookup: %w", err)
		}
	}

	vec, embErr := s.embedder.Embed(doc.Text)
	if embErr != nil {
		// Embedding failure is non-fatal — the row is still in documents
		// and FTS5; only the vec path is degraded. Callers see reduced
		// semantic quality rather than a hard error. Logged for ops.
		log.Printf("[memory] Index: embed failed (scope=%q id=%q): %v — vec skipped", scope, doc.ID, embErr)
		return nil
	}
	if len(vec) != s.embedDim {
		return fmt.Errorf("memory: Index: embedder returned %d dims, expected %d", len(vec), s.embedDim)
	}

	// UPSERT into vec0: delete old row (if any) then insert. vec0 does not
	// support ON CONFLICT.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM documents_vec WHERE rowid = ?`, rowID); err != nil {
		return fmt.Errorf("memory: Index: vec delete: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO documents_vec (rowid, embedding) VALUES (?, ?)`,
		rowID, serializeVec(vec)); err != nil {
		return fmt.Errorf("memory: Index: vec insert: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Search(ctx context.Context, scope Scope, query string, k int) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if k < 0 {
		return nil, errors.New("memory: Search: k must be >= 0")
	}
	if k == 0 {
		return nil, nil
	}

	// Vector path: embed the query, ask sqlite-vec for nearest neighbours
	// scoped to the requested Scope, then map back to Documents.
	if s.embedder != nil && s.embedder.IsReady() && strings.TrimSpace(query) != "" {
		if hits, err := s.searchVec(ctx, scope, query, k); err != nil {
			log.Printf("[memory] Search: vec path failed, falling back to LIKE: %v", err)
		} else {
			return hits, nil
		}
	}

	// LIKE substring fallback — used when no embedder is configured, the
	// embedder is not ready, or the query is empty. Kept so tests and
	// bootstrap runs without a model continue to work.
	q := query
	lower := strings.ToLower(q)

	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_id, text, metadata FROM documents
		 WHERE scope = ? AND (? = '' OR LOWER(text) LIKE ?)
		 ORDER BY inserted_at ASC`,
		string(scope), lower, "%"+lower+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		hit Hit
		idx int
	}
	var matches []scored
	idx := 0
	for rows.Next() {
		var d Document
		var meta string
		if err := rows.Scan(&d.ID, &d.Text, &meta); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &d.Metadata)
		}
		score := float32(1.0)
		if q != "" {
			score = float32(len(q)) / float32(len(d.Text)+1)
		}
		matches = append(matches, scored{Hit{Document: d, Score: score}, idx})
		idx++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}

	// Sort by score desc, then insertion order asc (stable).
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && scoredLess(matches[j], matches[j-1]); j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}

	if len(matches) > k {
		matches = matches[:k]
	}
	out := make([]Hit, len(matches))
	for i, m := range matches {
		out[i] = m.hit
	}
	return out, nil
}

func scoredLess(a, b struct {
	hit Hit
	idx int
}) bool {
	if a.hit.Score != b.hit.Score {
		return a.hit.Score > b.hit.Score
	}
	return a.idx < b.idx
}

// searchVec runs a cosine-distance nearest-neighbour lookup over documents_vec
// and returns the matching Documents. Restricted to the requested scope — we
// over-fetch (k * 4, capped) from vec0 and filter post-hoc because sqlite-vec
// does not expose a SQL WHERE clause on the index itself.
func (s *SQLiteStore) searchVec(ctx context.Context, scope Scope, query string, k int) ([]Hit, error) {
	qv, err := s.embedder.EmbedQuery(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(qv) != s.embedDim {
		return nil, fmt.Errorf("embedder returned %d dims, expected %d", len(qv), s.embedDim)
	}

	// Over-fetch to absorb the per-scope filter. 4x is a heuristic — same
	// ballpark as memstore. Capped so we don't pull the whole index if a
	// caller asks for k=10000.
	fetch := k * 4
	if fetch < 32 {
		fetch = 32
	}
	if fetch > 1024 {
		fetch = 1024
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT d.doc_id, d.text, d.metadata, v.distance
		FROM documents_vec v
		JOIN documents d ON d.rowid = v.rowid
		WHERE v.embedding MATCH ? AND d.scope = ? AND k = ?
		ORDER BY v.distance ASC`,
		serializeVec(qv), string(scope), fetch)
	if err != nil {
		return nil, fmt.Errorf("vec query: %w", err)
	}
	defer rows.Close()

	var out []Hit
	for rows.Next() {
		var d Document
		var meta string
		var dist float64
		if err := rows.Scan(&d.ID, &d.Text, &meta, &dist); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &d.Metadata)
		}
		// Cosine distance → similarity score in [0, 1] (higher == more
		// relevant), matching the contract on Hit.Score.
		score := float32(1.0 - dist)
		if score < 0 {
			score = 0
		}
		out = append(out, Hit{Document: d, Score: score})
		if len(out) >= k {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (s *SQLiteStore) GetDocument(ctx context.Context, scope Scope, docID string) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scope == "" {
		return nil, errors.New("memory: GetDocument: empty scope")
	}
	if docID == "" {
		return nil, errors.New("memory: GetDocument: empty docID")
	}
	var d Document
	var meta string
	err := s.db.QueryRowContext(ctx,
		`SELECT doc_id, text, metadata FROM documents WHERE scope = ? AND doc_id = ?`,
		string(scope), docID).Scan(&d.ID, &d.Text, &meta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if meta != "" && meta != "{}" {
		_ = json.Unmarshal([]byte(meta), &d.Metadata)
	}
	return &d, nil
}

func (s *SQLiteStore) ListDocuments(ctx context.Context, scope Scope, limit int) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scope == "" {
		return nil, errors.New("memory: ListDocuments: empty scope")
	}
	if limit <= 0 {
		return nil, errors.New("memory: ListDocuments: limit must be > 0")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_id, text, metadata FROM documents
		 WHERE scope = ?
		 ORDER BY inserted_at ASC, rowid ASC
		 LIMIT ?`,
		string(scope), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Document
	for rows.Next() {
		var d Document
		var meta string
		if err := rows.Scan(&d.ID, &d.Text, &meta); err != nil {
			return nil, err
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &d.Metadata)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) DeleteDocument(ctx context.Context, scope Scope, docID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if scope == "" {
		return false, errors.New("memory: DeleteDocument: empty scope")
	}
	if docID == "" {
		return false, errors.New("memory: DeleteDocument: empty docID")
	}

	// Look up the rowid first — needed to clean the vec table because vec0
	// is a virtual table that doesn't participate in regular FK cascades.
	// documents_fts is handled by the AFTER DELETE trigger (see
	// migrateVecSchema).
	var rowID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT rowid FROM documents WHERE scope = ? AND doc_id = ?`,
		string(scope), docID).Scan(&rowID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Drop the vec row before deleting from documents — if the delete
	// trigger on documents fires while the vec row is still present and
	// the process crashes between the two DELETEs, reopening the DB would
	// leave an orphan vec embedding. Ordering the vec DELETE first means
	// the worst case is a document with no embedding (benign — Search
	// just won't rank it).
	if s.embedDim > 0 {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM documents_vec WHERE rowid = ?`, rowID); err != nil {
			return false, fmt.Errorf("memory: DeleteDocument: vec cleanup: %w", err)
		}
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM documents WHERE scope = ? AND doc_id = ?`,
		string(scope), docID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Preferences ---------------------------------------------------------------

func (s *SQLiteStore) GetPref(ctx context.Context, key string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("memory: GetPref: empty key")
	}
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM prefs WHERE key = ?`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var v Value
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("memory: GetPref: unmarshal: %w", err)
	}
	return v, nil
}

func (s *SQLiteStore) SetPref(ctx context.Context, key string, val Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("memory: SetPref: empty key")
	}
	if val == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM prefs WHERE key = ?`, key)
		return err
	}
	b, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("memory: SetPref: marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO prefs (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(b))
	return err
}
