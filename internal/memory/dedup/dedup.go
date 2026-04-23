// Package dedup provides a small deduplication layer on top of
// memory.Store.Index. It exists because the Extractor and Consolidator
// (pre-#337 on memstore.Store) relied on FTS5-based fuzzy dedup to avoid
// re-storing near-identical facts on every run. The unified
// memory.Store contract is intentionally narrow and does not offer
// fuzzy-match out of the box, so the policy lives here instead.
//
// Design choices:
//
//   - Exact dedup is free: docID is derived from sha256(text), so re-
//     indexing the same text produces a single upsert instead of a new
//     row. This matches the ingest-adapter strategy in cmd/alf-daemon
//     and keeps re-extraction idempotent.
//
//   - Near-dup detection is opt-in via NearDupThreshold. When set, a
//     memory.Search is issued before Index; if the top hit's similarity
//     score meets or exceeds the threshold, the write is skipped.
//     Semantics depend on whether the Store has an embedder configured:
//     vec cosine when yes, LIKE score when no (degraded fallback).
//
//   - Scope boundaries are honoured: dedup only looks at the scope
//     passed in, so a "fact" and a "preference" with the same text are
//     treated as distinct.
package dedup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
)

// Options tunes the dedup policy.
type Options struct {
	// NearDupThreshold is the minimum similarity score at which a search
	// hit is treated as a duplicate. Score semantics:
	//
	//   - With an embedder: cosine similarity in [0, 1]; 0.85+ is
	//     "effectively the same idea."
	//   - Without an embedder: LIKE match quality (len(query)/len(text));
	//     set low or disable entirely (set to 0).
	//
	// Leave 0 to disable near-dup detection and only catch byte-identical
	// duplicates via the hash-based docID.
	NearDupThreshold float32

	// Source is recorded in the document metadata ("source" key). Empty
	// is legal.
	Source string

	// Now is a test seam for the "created_at" metadata timestamp. Leave
	// nil to use the caller's local time at Index.
	Now func() string
}

// DocID returns the canonical hash-derived document ID for text. Exposed
// so callers can pre-check existence or reference a memory by stable
// content key without re-hashing locally.
func DocID(text string) string {
	sum := sha256.Sum256([]byte(text))
	// 12 bytes is the same budget the ingest adapter uses — short enough
	// to log, long enough to avoid collision in any realistic corpus.
	return "mem-" + hex.EncodeToString(sum[:12])
}

// Result describes the outcome of IndexWithDedup. Stored == false means
// the call was a no-op because the text matched an existing document.
type Result struct {
	DocID   string
	Stored  bool // true iff Index was actually called
	Reason  string // "exact", "near", "" (stored)
	Near    *memory.Hit // populated when Reason == "near"
}

// IndexWithDedup writes doc into store under scope unless an existing
// document represents the same idea.
//
// Pipeline:
//  1. Derive docID = DocID(doc.Text). If store already has that id in
//     scope → exact duplicate, return Stored=false/Reason="exact".
//  2. If opts.NearDupThreshold > 0 → Search the scope for doc.Text;
//     if top hit.Score >= threshold → near duplicate, return
//     Stored=false/Reason="near" with the hit attached.
//  3. Otherwise: populate doc.ID with the hash-derived id, enrich
//     metadata with Source + created_at, call store.Index, return
//     Stored=true.
//
// Any error from the underlying Store is returned unchanged.
func IndexWithDedup(ctx context.Context, store memory.Store, scope memory.Scope, doc memory.Document, opts Options) (Result, error) {
	if store == nil {
		return Result{}, errors.New("dedup: nil store")
	}
	if scope == "" {
		return Result{}, errors.New("dedup: empty scope")
	}
	trimmed := strings.TrimSpace(doc.Text)
	if trimmed == "" {
		return Result{}, errors.New("dedup: empty text")
	}

	id := DocID(doc.Text)

	// Exact-dup check: O(1) key lookup against the stable hash docID.
	existing, err := store.GetDocument(ctx, scope, id)
	if err != nil {
		return Result{}, err
	}
	if existing != nil {
		return Result{DocID: id, Stored: false, Reason: "exact"}, nil
	}

	// Near-dup check: only issued when the caller opts in, to keep the
	// no-threshold path cheap (one GetDocument + one Index).
	if opts.NearDupThreshold > 0 {
		hits, err := store.Search(ctx, scope, doc.Text, 1)
		if err != nil {
			return Result{}, err
		}
		if len(hits) > 0 && hits[0].Score >= opts.NearDupThreshold {
			h := hits[0]
			return Result{DocID: id, Stored: false, Reason: "near", Near: &h}, nil
		}
	}

	// Build the outbound document. The caller's supplied ID is
	// overwritten — dedup owns the ID namespace under this API.
	doc.ID = id
	if doc.Metadata == nil {
		doc.Metadata = map[string]string{}
	}
	if opts.Source != "" {
		doc.Metadata["source"] = opts.Source
	}
	if _, ok := doc.Metadata["created_at"]; !ok {
		if opts.Now != nil {
			doc.Metadata["created_at"] = opts.Now()
		}
	}

	if err := store.Index(ctx, scope, doc); err != nil {
		return Result{}, err
	}
	return Result{DocID: id, Stored: true}, nil
}
