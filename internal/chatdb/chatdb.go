package chatdb

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps a SQLite database for chat message persistence.
type DB struct {
	db *sql.DB
}

// Message represents a chat message with optional content blocks, reactions, and media.
type Message struct {
	ID         string         `json:"id"`
	ConvID     string         `json:"conv_id"`
	Role       string         `json:"role"`
	Text       string         `json:"text"`
	Source     string         `json:"source"`
	Model      string         `json:"model,omitempty"`
	Tier       string         `json:"tier,omitempty"`
	CostUSD    float64        `json:"cost_usd,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	ReplyTo    string         `json:"reply_to,omitempty"`
	CreatedAt  time.Time      `json:"ts"`
	Blocks     []ContentBlock `json:"content_blocks,omitempty"`
	Reactions  []Reaction     `json:"reactions,omitempty"`
	Media      []MediaRef     `json:"media,omitempty"`
}

// ContentBlock stores a single content block (text, thinking, tool_use, tool_result).
type ContentBlock struct {
	BlockIndex int    `json:"block_index"`
	BlockType  string `json:"type"`
	Text       string `json:"text,omitempty"`
	Name       string `json:"name,omitempty"`
	Input      string `json:"input,omitempty"`
	ToolID     string `json:"tool_id,omitempty"`
	Output     string `json:"output,omitempty"`
}

// Reaction stores an emoji reaction on a message.
type Reaction struct {
	Emoji  string `json:"emoji"`
	Source string `json:"from"`
}

// MediaRef references an uploaded media file.
type MediaRef struct {
	UploadID  string `json:"upload_id"`
	FileName  string `json:"file_name"`
	MimeType  string `json:"mime_type"`
	MediaType string `json:"type"`
	FilePath  string `json:"-"`
	URL       string `json:"url,omitempty"`
}

// ConversationInfo summarises a conversation for listing.
type ConversationInfo struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastMessage time.Time `json:"last_message"`
	Archived    bool      `json:"archived"`
	MsgCount    int       `json:"msg_count"`
}

const schema = `
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

CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conv_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_source ON messages(source, created_at);

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
`

// New opens or creates the chat database in dataDir/logs/chat.db.
func New(dataDir string) (*DB, error) {
	dir := filepath.Join(dataDir, "logs")
	os.MkdirAll(dir, 0o755)
	dbPath := filepath.Join(dir, "chat.db")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("chatdb open: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("chatdb WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("chatdb fk: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("chatdb schema: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// EnsureConversation creates a conversation if it doesn't exist (idempotent).
func (d *DB) EnsureConversation(id, title, source string) error {
	if source == "" {
		source = "cc"
	}
	_, err := d.db.Exec(
		`INSERT OR IGNORE INTO conversations (id, title, source) VALUES (?, ?, ?)`,
		id, title, source,
	)
	return err
}

// InsertMessage inserts a message along with its content blocks and media in a single transaction.
func (d *DB) InsertMessage(msg Message) error {
	if msg.Source == "" {
		msg.Source = "cc"
	}
	ts := msg.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT OR REPLACE INTO messages (id, conv_id, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.ConvID, msg.Role, msg.Text, msg.Source,
		msg.Model, msg.Tier, msg.CostUSD, msg.DurationMs,
		msg.SessionID, msg.ReplyTo, ts,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	if len(msg.Blocks) > 0 {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO content_blocks (message_id, block_index, block_type, text, name, input, tool_id, output)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return fmt.Errorf("prepare blocks: %w", err)
		}
		defer stmt.Close()
		for _, b := range msg.Blocks {
			if _, err := stmt.Exec(msg.ID, b.BlockIndex, b.BlockType, b.Text, b.Name, b.Input, b.ToolID, b.Output); err != nil {
				return fmt.Errorf("insert block: %w", err)
			}
		}
	}

	if len(msg.Media) > 0 {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO media (upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, url)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return fmt.Errorf("prepare media: %w", err)
		}
		defer stmt.Close()
		for _, m := range msg.Media {
			if _, err := stmt.Exec(m.UploadID, msg.ID, msg.ConvID, m.FileName, m.MimeType, m.MediaType, m.FilePath, m.URL); err != nil {
				return fmt.Errorf("insert media: %w", err)
			}
		}
	}

	// Update conversation updated_at.
	tx.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, ts, msg.ConvID)

	return tx.Commit()
}

// Get retrieves a single message by ID, including blocks and reactions.
func (d *DB) Get(msgID string) (*Message, error) {
	row := d.db.QueryRow(
		`SELECT id, conv_id, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at
		 FROM messages WHERE id = ?`, msgID,
	)
	var msg Message
	if err := row.Scan(&msg.ID, &msg.ConvID, &msg.Role, &msg.Text, &msg.Source,
		&msg.Model, &msg.Tier, &msg.CostUSD, &msg.DurationMs,
		&msg.SessionID, &msg.ReplyTo, &msg.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	msg.Blocks = d.loadBlocks(msgID)
	msg.Reactions = d.loadReactions(msgID)
	msg.Media = d.loadMedia(msgID)
	return &msg, nil
}

// AddReaction adds a reaction to a message (idempotent on same emoji+source).
func (d *DB) AddReaction(msgID, emoji, source string) error {
	_, err := d.db.Exec(
		`INSERT OR IGNORE INTO reactions (message_id, emoji, source) VALUES (?, ?, ?)`,
		msgID, emoji, source,
	)
	return err
}

// History returns messages for a conversation, paginated by limit and before timestamp.
// Returns messages in chronological order. If convID is empty, returns all messages.
func (d *DB) History(convID string, limit int, before time.Time) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if convID == "" && before.IsZero() {
		rows, err = d.db.Query(
			`SELECT id, conv_id, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at
			 FROM messages ORDER BY created_at DESC LIMIT ?`, limit,
		)
	} else if convID == "" {
		rows, err = d.db.Query(
			`SELECT id, conv_id, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at
			 FROM messages WHERE created_at < ? ORDER BY created_at DESC LIMIT ?`, before, limit,
		)
	} else if before.IsZero() {
		rows, err = d.db.Query(
			`SELECT id, conv_id, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at
			 FROM messages WHERE conv_id = ? ORDER BY created_at DESC LIMIT ?`, convID, limit,
		)
	} else {
		rows, err = d.db.Query(
			`SELECT id, conv_id, role, text, source, model, tier, cost_usd, duration_ms, session_id, reply_to, created_at
			 FROM messages WHERE conv_id = ? AND created_at < ? ORDER BY created_at DESC LIMIT ?`, convID, before, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Text, &m.Source,
			&m.Model, &m.Tier, &m.CostUSD, &m.DurationMs,
			&m.SessionID, &m.ReplyTo, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}

	// Reverse to chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	// Load blocks and reactions per message.
	for i := range msgs {
		msgs[i].Blocks = d.loadBlocks(msgs[i].ID)
		msgs[i].Reactions = d.loadReactions(msgs[i].ID)
		msgs[i].Media = d.loadMedia(msgs[i].ID)
	}

	if msgs == nil {
		msgs = []Message{}
	}
	return msgs, nil
}

// Conversations lists conversations, optionally filtered by source.
func (d *DB) Conversations(source string, includeArchived bool) ([]ConversationInfo, error) {
	q := `SELECT c.id, c.title, c.source, c.created_at, c.updated_at, c.archived,
	             COALESCE(s.cnt, 0), COALESCE(s.last_msg, c.created_at)
	      FROM conversations c
	      LEFT JOIN (
	          SELECT conv_id, COUNT(*) AS cnt, MAX(created_at) AS last_msg
	          FROM messages GROUP BY conv_id
	      ) s ON s.conv_id = c.id`

	var conditions []string
	var args []any

	if source != "" {
		conditions = append(conditions, "c.source = ?")
		args = append(args, source)
	}
	if !includeArchived {
		conditions = append(conditions, "c.archived = 0")
	}
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += " ORDER BY c.created_at ASC"

	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []ConversationInfo
	for rows.Next() {
		var c ConversationInfo
		var createdAt, updatedAt, lastMsg string
		if err := rows.Scan(&c.ID, &c.Title, &c.Source, &createdAt, &updatedAt,
			&c.Archived, &c.MsgCount, &lastMsg); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(createdAt)
		c.UpdatedAt = parseTime(updatedAt)
		c.LastMessage = parseTime(lastMsg)
		convs = append(convs, c)
	}
	if convs == nil {
		convs = []ConversationInfo{}
	}
	return convs, nil
}

// parseTime parses a SQLite timestamp string into time.Time.
func parseTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999-07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// UpdateConversation updates a conversation's title.
func (d *DB) UpdateConversation(id, title string) error {
	_, err := d.db.Exec(
		`UPDATE conversations SET title = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		title, id,
	)
	return err
}

// ArchiveConversation marks a conversation as archived.
func (d *DB) ArchiveConversation(id string) error {
	_, err := d.db.Exec(`UPDATE conversations SET archived = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// DeleteConversation hard-deletes a conversation and all related data (cascade).
func (d *DB) DeleteConversation(id string) error {
	_, err := d.db.Exec(`DELETE FROM conversations WHERE id = ?`, id)
	return err
}

// SessionStats returns the latest interactive session ID with its message count and total cost.
// Sessions whose session_id starts with excludePrefix are skipped.
func (d *DB) SessionStats(excludePrefix string) (sessionID string, count int, cost float64, err error) {
	// Find latest non-excluded session.
	row := d.db.QueryRow(
		`SELECT session_id FROM messages
		 WHERE session_id != '' AND session_id NOT LIKE ?
		 ORDER BY created_at DESC LIMIT 1`,
		excludePrefix+"%",
	)
	if err = row.Scan(&sessionID); err != nil {
		if err == sql.ErrNoRows {
			return "", 0, 0, nil
		}
		return
	}

	row = d.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(cost_usd), 0) FROM messages WHERE session_id = ?`,
		sessionID,
	)
	err = row.Scan(&count, &cost)
	return
}

// loadBlocks returns content blocks for a message, ordered by block_index.
func (d *DB) loadBlocks(msgID string) []ContentBlock {
	rows, err := d.db.Query(
		`SELECT block_index, block_type, text, name, input, tool_id, output
		 FROM content_blocks WHERE message_id = ? ORDER BY block_index`, msgID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var blocks []ContentBlock
	for rows.Next() {
		var b ContentBlock
		rows.Scan(&b.BlockIndex, &b.BlockType, &b.Text, &b.Name, &b.Input, &b.ToolID, &b.Output)
		blocks = append(blocks, b)
	}
	return blocks
}

// loadReactions returns reactions for a message.
func (d *DB) loadReactions(msgID string) []Reaction {
	rows, err := d.db.Query(
		`SELECT emoji, source FROM reactions WHERE message_id = ?`, msgID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var reactions []Reaction
	for rows.Next() {
		var r Reaction
		rows.Scan(&r.Emoji, &r.Source)
		reactions = append(reactions, r)
	}
	return reactions
}

// loadMedia returns media refs for a message.
func (d *DB) loadMedia(msgID string) []MediaRef {
	rows, err := d.db.Query(
		`SELECT upload_id, file_name, mime_type, media_type, file_path, url
		 FROM media WHERE message_id = ?`, msgID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var refs []MediaRef
	for rows.Next() {
		var m MediaRef
		rows.Scan(&m.UploadID, &m.FileName, &m.MimeType, &m.MediaType, &m.FilePath, &m.URL)
		refs = append(refs, m)
	}
	return refs
}

// InsertMediaRef associates a media reference with an existing message.
func (d *DB) InsertMediaRef(ref MediaRef, messageID, convID string) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO media (upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, url)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ref.UploadID, messageID, convID, ref.FileName, ref.MimeType, ref.MediaType, ref.FilePath, ref.URL,
	)
	return err
}

// GetMediaByUploadID retrieves a single media reference by upload ID.
func (d *DB) GetMediaByUploadID(uploadID string) (*MediaRef, error) {
	row := d.db.QueryRow(
		`SELECT upload_id, file_name, mime_type, media_type, file_path, url
		 FROM media WHERE upload_id = ?`, uploadID,
	)
	var m MediaRef
	if err := row.Scan(&m.UploadID, &m.FileName, &m.MimeType, &m.MediaType, &m.FilePath, &m.URL); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// ExpiredMediaForConversation returns media refs older than the cutoff for a conversation.
func (d *DB) ExpiredMediaForConversation(convID string, olderThan time.Time) []MediaRef {
	rows, err := d.db.Query(
		`SELECT upload_id, file_name, mime_type, media_type, file_path, url
		 FROM media WHERE conv_id = ? AND created_at < ?`, convID, olderThan,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var refs []MediaRef
	for rows.Next() {
		var m MediaRef
		rows.Scan(&m.UploadID, &m.FileName, &m.MimeType, &m.MediaType, &m.FilePath, &m.URL)
		refs = append(refs, m)
	}
	return refs
}

// DeleteMedia removes a media row by upload ID.
func (d *DB) DeleteMedia(uploadID string) error {
	_, err := d.db.Exec(`DELETE FROM media WHERE upload_id = ?`, uploadID)
	return err
}

// Exec runs a raw SQL statement. Intended for tests that need to backdate timestamps.
func (d *DB) Exec(query string, args ...any) error {
	_, err := d.db.Exec(query, args...)
	return err
}

// jsonlMessage matches the old ChatStore JSONL format for migration.
type jsonlMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"ts"`
	Model     string    `json:"model"`
	Tier      string    `json:"tier"`
	CostUSD   float64   `json:"cost_usd"`
	SessionID string    `json:"session_id"`
	ConvID    string    `json:"conv_id"`
	ReplyTo   string    `json:"reply_to"`
	Media     []struct {
		UploadID string `json:"upload_id"`
		Type     string `json:"type"`
		FileName string `json:"file_name"`
		MimeType string `json:"mime_type"`
		URL      string `json:"url"`
	} `json:"media"`
	Reactions []struct {
		Emoji string `json:"emoji"`
		From  string `json:"from"`
	} `json:"reactions"`
}

// MigrateFromJSONL imports messages from a legacy chat_messages.jsonl file.
// After successful migration, the file is renamed to .jsonl.migrated.
// Safe to call multiple times — skips if already migrated or file doesn't exist.
func (d *DB) MigrateFromJSONL(jsonlPath string) error {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil // file doesn't exist or already migrated
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)

	// Track conversations to create.
	convSeen := make(map[string]bool)
	var imported int

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("migration tx: %w", err)
	}
	defer tx.Rollback()

	// Ensure foreign keys don't block bulk insert — create convs first.
	msgStmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO messages (id, conv_id, role, text, source, model, tier, cost_usd, session_id, reply_to, created_at)
		 VALUES (?, ?, ?, ?, 'cc', ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("migration prepare: %w", err)
	}
	defer msgStmt.Close()

	convStmt, err := tx.Prepare(`INSERT OR IGNORE INTO conversations (id, title, source) VALUES (?, '', 'cc')`)
	if err != nil {
		return fmt.Errorf("migration prepare conv: %w", err)
	}
	defer convStmt.Close()

	reactStmt, err := tx.Prepare(`INSERT OR IGNORE INTO reactions (message_id, emoji, source) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("migration prepare react: %w", err)
	}
	defer reactStmt.Close()

	for scanner.Scan() {
		var m jsonlMessage
		if json.Unmarshal(scanner.Bytes(), &m) != nil || m.ID == "" {
			continue
		}
		convID := m.ConvID
		if convID == "" {
			convID = "_default"
		}
		if !convSeen[convID] {
			convStmt.Exec(convID)
			convSeen[convID] = true
		}

		msgStmt.Exec(m.ID, convID, m.Role, m.Text, m.Model, m.Tier, m.CostUSD, m.SessionID, m.ReplyTo, m.Timestamp)

		for _, r := range m.Reactions {
			reactStmt.Exec(m.ID, r.Emoji, r.From)
		}

		imported++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration commit: %w", err)
	}

	// Rename source file to mark migration complete.
	if imported > 0 {
		migratedPath := jsonlPath + ".migrated"
		if err := os.Rename(jsonlPath, migratedPath); err != nil {
			log.Printf("[chatdb] migration: imported %d messages but rename failed: %v", imported, err)
		} else {
			log.Printf("[chatdb] migration: imported %d messages from JSONL", imported)
		}
	}

	return nil
}
