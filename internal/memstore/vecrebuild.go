package memstore

import (
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
)

// memory_meta stores schema metadata for detecting dims mismatches.
// Created lazily on first dims check.

func (s *Store) migrateMetaTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS memory_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
}

// storedDims returns the embedding dimensions recorded in the DB, or 0 if unknown.
func (s *Store) storedDims() int {
	var val string
	err := s.db.QueryRow(`SELECT value FROM memory_meta WHERE key = 'dims'`).Scan(&val)
	if err != nil {
		return 0
	}
	var dims int
	fmt.Sscanf(val, "%d", &dims)
	return dims
}

func (s *Store) setStoredDims(dims int) {
	s.db.Exec(`INSERT OR REPLACE INTO memory_meta (key, value) VALUES ('dims', ?)`,
		fmt.Sprintf("%d", dims))
}

// RebuildProgress returns the current vector rebuild progress (0-100), or -1 if no rebuild in progress.
func (s *Store) RebuildProgress() int {
	var val string
	err := s.db.QueryRow(`SELECT value FROM memory_meta WHERE key = 'rebuild_progress'`).Scan(&val)
	if err != nil {
		return -1
	}
	var pct int
	fmt.Sscanf(val, "%d", &pct)
	return pct
}

func (s *Store) setRebuildProgress(pct int) {
	s.db.Exec(`INSERT OR REPLACE INTO memory_meta (key, value) VALUES ('rebuild_progress', ?)`,
		fmt.Sprintf("%d", pct))
}

func (s *Store) clearRebuildProgress() {
	s.db.Exec(`DELETE FROM memory_meta WHERE key = 'rebuild_progress'`)
}

// CheckDims verifies that the embedder dimensions match the stored vec table.
// If mismatched, triggers a background rebuild. Safe to call from main goroutine.
func (s *Store) CheckDims() {
	if s.embedder == nil || !s.embedder.IsReady() {
		return
	}

	if err := s.migrateMetaTable(); err != nil {
		log.Printf("memstore: meta table migration failed: %v", err)
		return
	}

	embedDims := s.embedder.Dims()
	dbDims := s.storedDims()

	if dbDims == 0 {
		// First time — record current dims, no rebuild needed.
		s.setStoredDims(embedDims)
		log.Printf("memstore: recorded embedding dims=%d", embedDims)
		return
	}

	if dbDims == embedDims {
		return // Match — nothing to do.
	}

	log.Printf("memstore: dims mismatch detected (db=%d, embedder=%d) — starting background rebuild", dbDims, embedDims)
	go s.rebuildVectors(embedDims)
}

// rebuildVectors re-embeds all memories into a new vec table with the correct dimensions.
// During rebuild, searches continue using the old table. Atomic swap at the end.
func (s *Store) rebuildVectors(newDims int) {
	s.setRebuildProgress(0)

	// Count total memories to embed.
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&total); err != nil {
		log.Printf("memstore: rebuild failed to count memories: %v", err)
		s.clearRebuildProgress()
		return
	}
	// Create new vec table (constant name — never derived from user input).
	const newTable = "memory_vec_new"
	s.db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, newTable))
	_, err := s.db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE %s USING vec0(
		id INTEGER PRIMARY KEY,
		embedding float[%d] distance_metric=cosine
	)`, newTable, newDims))
	if err != nil {
		log.Printf("memstore: rebuild failed to create new vec table: %v", err)
		s.clearRebuildProgress()
		return
	}

	if total == 0 {
		s.finishRebuild(newDims)
		return
	}

	// Re-embed in batches.
	const batchSize = 50
	var done atomic.Int64
	offset := 0

	for offset < total {
		rows, err := s.db.Query(`SELECT id, text FROM memories ORDER BY id LIMIT ? OFFSET ?`, batchSize, offset)
		if err != nil {
			log.Printf("memstore: rebuild query failed at offset %d: %v", offset, err)
			s.db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, newTable))
			s.clearRebuildProgress()
			return
		}

		type memRow struct {
			id   int64
			text string
		}
		var batch []memRow
		for rows.Next() {
			var m memRow
			rows.Scan(&m.id, &m.text)
			batch = append(batch, m)
		}
		rows.Close()

		if len(batch) == 0 {
			break
		}

		// Embed batch.
		texts := make([]string, len(batch))
		for i, m := range batch {
			texts[i] = m.text
		}

		s.mu.RLock()
		emb := s.embedder
		s.mu.RUnlock()

		if emb == nil || !emb.IsReady() {
			log.Printf("memstore: rebuild aborted — embedder no longer available")
			s.db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, newTable))
			s.clearRebuildProgress()
			return
		}

		vecs, err := emb.EmbedBatch(texts)
		if err != nil {
			log.Printf("memstore: rebuild embed failed at offset %d: %v", offset, err)
			s.db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, newTable))
			s.clearRebuildProgress()
			return
		}

		// Insert into new table.
		for i, m := range batch {
			vecJSON, _ := json.Marshal(vecs[i])
			if _, err := s.db.Exec(
				fmt.Sprintf(`INSERT INTO %s (id, embedding) VALUES (?, ?)`, newTable),
				m.id, string(vecJSON),
			); err != nil {
				log.Printf("memstore: rebuild insert failed for id=%d: %v", m.id, err)
				// Continue — partial rebuild is better than none.
			}
		}

		offset += len(batch)
		done.Add(int64(len(batch)))
		pct := int(done.Load() * 100 / int64(total))
		s.setRebuildProgress(pct)

		if pct%25 == 0 || offset >= total {
			log.Printf("memstore: rebuild progress %d%% (%d/%d)", pct, done.Load(), total)
		}
	}

	s.finishRebuild(newDims)
}

// finishRebuild performs the atomic table swap.
func (s *Store) finishRebuild(newDims int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Atomic swap: drop old, rename new.
	s.db.Exec(`DROP TABLE IF EXISTS memory_vec`)
	_, err := s.db.Exec(`ALTER TABLE memory_vec_new RENAME TO memory_vec`)
	if err != nil {
		log.Printf("memstore: rebuild swap failed: %v", err)
		s.clearRebuildProgress()
		return
	}

	s.setStoredDims(newDims)
	s.clearRebuildProgress()
	log.Printf("memstore: rebuild complete — switched to dims=%d", newDims)
}
