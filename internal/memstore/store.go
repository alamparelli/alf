package memstore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// Memory represents a single stored memory entry.
type Memory struct {
	ID        int64
	Text      string
	Type      string         // "fact", "summary", "preference", "decision"
	Source    string         // "extractor", "claude", "user"
	Metadata  map[string]any
	CreatedAt time.Time
	Distance  float64        // populated by Search(), 0 otherwise
}

// DedupConfig holds configurable deduplication thresholds.
type DedupConfig struct {
	TextThreshold   float64 // Jaccard similarity threshold (default 0.7)
	CosineThreshold float64 // cosine distance threshold (default 0.15)
}

// Store manages the semantic memory database with sqlite-vec + FTS5.
type Store struct {
	db       *sql.DB
	embedder EmbedderI
	dedup    atomic.Value // DedupConfig, swappable at runtime (hot reload)
	mu       sync.RWMutex
}

// dedupCfg returns the current dedup config (lock-free read).
func (s *Store) dedupCfg() DedupConfig {
	if v := s.dedup.Load(); v != nil {
		return v.(DedupConfig)
	}
	return DedupConfig{TextThreshold: 0.7, CosineThreshold: 0.15}
}

// SetDedupConfig atomically updates dedup thresholds. Values <= 0 keep existing.
// Returns the effective config after merge.
func (s *Store) SetDedupConfig(cfg DedupConfig) DedupConfig {
	cur := s.dedupCfg()
	if cfg.TextThreshold > 0 {
		cur.TextThreshold = cfg.TextThreshold
	}
	if cfg.CosineThreshold > 0 {
		cur.CosineThreshold = cfg.CosineThreshold
	}
	s.dedup.Store(cur)
	return cur
}

// New opens (or creates) the memory database and initialises the schema.
// Optional DedupConfig can be passed; if nil, defaults are used.
func New(dbPath string, embedder EmbedderI, dedupCfg ...DedupConfig) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL for concurrent access from daemon + tool processes.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	dedup := DedupConfig{TextThreshold: 0.7, CosineThreshold: 0.15}
	if len(dedupCfg) > 0 {
		if dedupCfg[0].TextThreshold > 0 {
			dedup.TextThreshold = dedupCfg[0].TextThreshold
		}
		if dedupCfg[0].CosineThreshold > 0 {
			dedup.CosineThreshold = dedupCfg[0].CosineThreshold
		}
	}

	s := &Store{db: db, embedder: embedder}
	s.dedup.Store(dedup)
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	count := s.Count()
	log.Printf("memstore: opened %s (%d memories, dedup: text=%.2f cosine=%.2f)", dbPath, count, dedup.TextThreshold, dedup.CosineThreshold)
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			text TEXT NOT NULL,
			type TEXT NOT NULL CHECK(type IN ('fact','summary','preference','decision','contact')),
			source TEXT NOT NULL DEFAULT 'extractor',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_vec USING vec0(
			id INTEGER PRIMARY KEY,
			embedding float[384] distance_metric=cosine
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			text, type, content=memories, content_rowid=id
		)`,
		// Triggers to keep FTS5 in sync.
		`CREATE TRIGGER IF NOT EXISTS memory_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memory_fts(rowid, text, type) VALUES (new.id, new.text, new.type);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memory_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, text, type) VALUES('delete', old.id, old.text, old.type);
		END`,
		`CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:60], err)
		}
	}

	// Migration: widen CHECK constraint if DB was created with older schema.
	// SQLite doesn't support ALTER CHECK, so we recreate the table.
	var checkSQL string
	row := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='memories'`)
	if err := row.Scan(&checkSQL); err == nil {
		if !strings.Contains(checkSQL, "'contact'") {
			log.Println("memstore: migrating memories table to add 'contact' type")
			migrationStmts := []string{
				`ALTER TABLE memories RENAME TO memories_old`,
				stmts[0], // recreate with new CHECK
				`INSERT INTO memories SELECT * FROM memories_old`,
				`DROP TABLE memories_old`,
			}
			for _, m := range migrationStmts {
				if _, err := s.db.Exec(m); err != nil {
					return fmt.Errorf("migration: %w", err)
				}
			}
			// Recreate triggers (dropped with table rename)
			for _, stmt := range stmts[3:5] { // the two triggers
				s.db.Exec(stmt)
			}
		}
	}

	return nil
}

// Store inserts a new memory. Returns the memory ID.
// Deduplicates by checking FTS5 for near-exact matches first.
func (s *Store) Store(text, memType, source string, meta map[string]any) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Dedup: check for near-exact match via FTS5.
	if s.hasDuplicate(text) {
		return 0, fmt.Errorf("duplicate memory detected")
	}

	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, _ := json.Marshal(meta)
	now := time.Now().Format(time.RFC3339)

	res, err := s.db.Exec(
		`INSERT INTO memories (text, type, source, metadata, created_at) VALUES (?, ?, ?, ?, ?)`,
		text, memType, source, string(metaJSON), now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert memory: %w", err)
	}

	id, _ := res.LastInsertId()

	// Embed and insert into vec table.
	if s.embedder != nil && s.embedder.IsReady() {
		vec, err := s.embedder.Embed(text)
		if err != nil {
			log.Printf("memstore: embed failed for memory #%d: %v", id, err)
		} else {
			vecJSON, _ := json.Marshal(vec)
			if _, err := s.db.Exec(
				`INSERT INTO memory_vec (id, embedding) VALUES (?, ?)`,
				id, string(vecJSON),
			); err != nil {
				log.Printf("memstore: vec insert failed for memory #%d: %v", id, err)
			}
		}
	}

	return id, nil
}

// Search performs semantic KNN search using vector embeddings.
func (s *Store) Search(query string, limit int) ([]Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.embedder == nil || !s.embedder.IsReady() {
		// Fall back to FTS5 if embedder not available.
		return s.textSearchLocked(query, limit)
	}

	vec, err := s.embedder.EmbedQuery(query)
	if err != nil {
		// Fall back to FTS5.
		log.Printf("memstore: embed query failed, falling back to FTS5: %v", err)
		return s.textSearchLocked(query, limit)
	}

	vecJSON, _ := json.Marshal(vec)

	rows, err := s.db.Query(`
		SELECT v.id, v.distance, m.text, m.type, m.source, m.metadata, m.created_at
		FROM memory_vec v
		JOIN memories m ON m.id = v.id
		WHERE v.embedding MATCH ?
		  AND k = ?
		ORDER BY v.distance
	`, string(vecJSON), limit)
	if err != nil {
		return nil, fmt.Errorf("vec search: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows, true)
}

// TextSearch performs keyword-based FTS5 search.
func (s *Store) TextSearch(query string, limit int) ([]Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.textSearchLocked(query, limit)
}

func (s *Store) textSearchLocked(query string, limit int) ([]Memory, error) {
	// Escape FTS5 special characters and make terms prefix-searchable.
	terms := strings.Fields(query)
	for i, t := range terms {
		terms[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"` + "*"
	}
	ftsQuery := strings.Join(terms, " ")

	rows, err := s.db.Query(`
		SELECT m.id, 0 AS distance, m.text, m.type, m.source, m.metadata, m.created_at
		FROM memory_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memory_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows, true)
}

// Recent returns the most recent memories.
func (s *Store) Recent(days, limit int) ([]Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := s.db.Query(`
		SELECT id, 0 AS distance, text, type, source, metadata, created_at
		FROM memories
		WHERE created_at >= ?
		ORDER BY created_at DESC
		LIMIT ?
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("recent: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows, true)
}

// Count returns the total number of stored memories.
func (s *Store) Count() int {
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&n)
	return n
}

// Delete removes a memory by ID.
func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Delete from vec table first (no cascade triggers for virtual tables).
	s.db.Exec("DELETE FROM memory_vec WHERE id = ?", id)

	// Delete from memories (triggers FTS5 sync).
	_, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
	return err
}

// SetEmbedder swaps the embedder implementation (e.g. on tier switch).
// Triggers a dims check which may start a background vector rebuild.
func (s *Store) SetEmbedder(embedder EmbedderI) {
	s.mu.Lock()
	s.embedder = embedder
	s.mu.Unlock()
	s.CheckDims()
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// hasDuplicate checks for near-duplicates using two strategies:
// 1. FTS5 keyword search + Jaccard similarity (catches lexical near-matches)
// 2. Cosine similarity on embeddings (catches semantic reformulations)
func (s *Store) hasDuplicate(text string) bool {
	dedup := s.dedupCfg()
	// Strategy 1: FTS5 + Jaccard for lexical near-matches.
	words := strings.Fields(text)
	if len(words) > 8 {
		words = words[:8]
	}
	searchTerms := make([]string, len(words))
	for i, w := range words {
		searchTerms[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	ftsQuery := strings.Join(searchTerms, " OR ")

	rows, err := s.db.Query(`
		SELECT m.text FROM memory_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memory_fts MATCH ?
		LIMIT 10
	`, ftsQuery)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var existing string
			rows.Scan(&existing)
			sim := textSimilarity(text, existing)
			if sim >= dedup.TextThreshold {
				truncNew := text
				if len(truncNew) > 80 {
					truncNew = truncNew[:80] + "..."
				}
				truncOld := existing
				if len(truncOld) > 80 {
					truncOld = truncOld[:80] + "..."
				}
				log.Printf("memstore: dedup-text rejected (sim=%.2f >= %.2f): %q ~ %q", sim, dedup.TextThreshold, truncNew, truncOld)
				return true
			}
		}
	}

	// Strategy 2: Cosine similarity on embeddings for semantic dedup.
	// Query top-3 nearest neighbors. If only 1 of 3 is close, it's likely a
	// false positive (shared keywords, different subject). All 3 close = true dup.
	if s.embedder != nil && s.embedder.IsReady() {
		vec, err := s.embedder.Embed(text)
		if err == nil {
			vecJSON, _ := json.Marshal(vec)
			rows, err := s.db.Query(`
				SELECT v.distance, m.text FROM memory_vec v
				JOIN memories m ON m.id = v.id
				WHERE v.embedding MATCH ?
				  AND k = 3
				ORDER BY v.distance
			`, string(vecJSON))
			if err == nil {
				defer rows.Close()
				var closest []struct {
					dist float64
					text string
				}
				for rows.Next() {
					var d float64
					var t string
					rows.Scan(&d, &t)
					closest = append(closest, struct {
						dist float64
						text string
					}{d, t})
				}

				if len(closest) > 0 && closest[0].dist < dedup.CosineThreshold {
					// Entity check: if the new fact and closest match share
					// fewer than half their proper nouns, it's a false positive.
					newEntities := extractEntities(text)
					oldEntities := extractEntities(closest[0].text)
					if len(newEntities) > 0 && entityOverlap(newEntities, oldEntities) < 0.5 {
						truncNew := truncate(text, 80)
						truncOld := truncate(closest[0].text, 80)
						log.Printf("memstore: dedup-cosine ALLOWED (dist=%.4f, entity overlap <50%%): %q vs %q", closest[0].dist, truncNew, truncOld)
						return false
					}

					// Top-K consensus: if only 1 of 3 is below threshold, weaker signal.
					belowCount := 0
					for _, c := range closest {
						if c.dist < dedup.CosineThreshold {
							belowCount++
						}
					}
					if belowCount == 1 && len(closest) >= 3 {
						truncNew := truncate(text, 80)
						truncOld := truncate(closest[0].text, 80)
						log.Printf("memstore: dedup-cosine ALLOWED (dist=%.4f, only 1/3 below threshold): %q vs %q", closest[0].dist, truncNew, truncOld)
						return false
					}

					truncNew := truncate(text, 80)
					truncOld := truncate(closest[0].text, 80)
					log.Printf("memstore: dedup-cosine rejected (dist=%.4f < %.2f, %d/3 below): %q vs %q", closest[0].dist, dedup.CosineThreshold, belowCount, truncNew, truncOld)
					return true
				}
			}
		}
	}

	return false
}

// extractEntities returns capitalized words that likely represent proper nouns.
// Language-agnostic: uses position and word length heuristics instead of
// language-specific stop word lists. Works for all Latin-script languages.
func extractEntities(text string) map[string]bool {
	entities := make(map[string]bool)
	words := strings.Fields(text)
	for i, w := range words {
		// Strip trailing punctuation
		clean := strings.TrimRight(w, ".,;:!?()[]{}\"'—–-")
		if len(clean) < 2 {
			continue
		}
		// Must start with uppercase ASCII
		if clean[0] < 'A' || clean[0] > 'Z' {
			continue
		}
		// Skip first word of text (always capitalized)
		if i == 0 {
			continue
		}
		// Skip words after sentence-ending punctuation (new sentence start)
		prev := words[i-1]
		if len(prev) > 0 {
			last := prev[len(prev)-1]
			if last == '.' || last == '!' || last == '?' || last == ':' {
				continue
			}
		}
		// Short capitalized words (≤3 chars) are usually articles/pronouns/
		// prepositions in any Latin-script language (The, Le, La, Un, De, Il, ...)
		if len(clean) <= 3 {
			continue
		}
		entities[clean] = true
	}
	return entities
}

// entityOverlap returns the fraction of entities in a that also appear in b.
func entityOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	var overlap int
	for e := range a {
		if b[e] {
			overlap++
		}
	}
	return float64(overlap) / float64(len(a))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// textSimilarity returns word-level Jaccard similarity between two texts.
func textSimilarity(a, b string) float64 {
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))

	setA := make(map[string]bool, len(wordsA))
	for _, w := range wordsA {
		setA[w] = true
	}
	setB := make(map[string]bool, len(wordsB))
	for _, w := range wordsB {
		setB[w] = true
	}

	var intersection int
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}

func scanMemories(rows *sql.Rows, hasDistance bool) ([]Memory, error) {
	var memories []Memory
	for rows.Next() {
		var m Memory
		var metaJSON string
		var createdAt string
		var distance float64

		if err := rows.Scan(&m.ID, &distance, &m.Text, &m.Type, &m.Source, &metaJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		m.Distance = distance
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		json.Unmarshal([]byte(metaJSON), &m.Metadata)

		memories = append(memories, m)
	}
	return memories, rows.Err()
}
