package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

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

	// nowFn is the injectable clock. Defaults to time.Now().UnixMilli.
	// Exposed only for tests in the same package.
	nowFn func() int64

	closeOnce sync.Once
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
func NewSQLiteStore(dataDir string) (*SQLiteStore, error) {
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO documents (scope, doc_id, text, metadata, inserted_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(scope, doc_id) DO UPDATE SET
		    text = excluded.text,
		    metadata = excluded.metadata,
		    inserted_at = excluded.inserted_at`,
		string(scope), doc.ID, doc.Text, meta, s.nowFn())
	return err
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

	// LIKE-based substring match — same pragma as InMem. The Step 1.3 work
	// replaces this with the FTS5 path inherited from memstore.
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
