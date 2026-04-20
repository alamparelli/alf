package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/dedup"
)

// Consolidator periodically deduplicates and reorganizes the memory store.
// It also acts as a fallback extractor for periods with no user sessions
// (e.g., overnight scheduled jobs).
//
// Post-#337 the only supported write path is memory.Store via
// dedup.IndexWithDedup. Callers MUST invoke SetMemoryBackend before
// RunOnce — otherwise consolidation is a no-op (logged, not fatal).
type Consolidator struct {
	extractor *Extractor
	provider  ExtractorProvider
	timeout   time.Duration

	mu               sync.Mutex
	memStore         memory.Store
	memScopes        []memory.Scope
	nearDupThreshold float32
}

// SetMemoryBackend wires consolidation onto memory.Store. scopes
// enumerate the memory types the Consolidator will walk (typically
// socketsrv.KnownScopes). threshold controls near-dup skipping when
// the Consolidator writes merged text — use the same value you pass
// to Extractor.SetMemoryBackend for symmetry.
func (c *Consolidator) SetMemoryBackend(store memory.Store, scopes []memory.Scope, threshold float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.memStore = store
	c.memScopes = append(c.memScopes[:0], scopes...)
	c.nearDupThreshold = threshold
}

// NewConsolidator creates a new consolidator. Call SetMemoryBackend
// before the first RunOnce() invocation.
func NewConsolidator(extractor *Extractor, prov ExtractorProvider, timeout time.Duration) *Consolidator {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &Consolidator{
		extractor: extractor,
		provider:  prov,
		timeout:   timeout,
	}
}

// memoryConsolidationAction is the wire shape the LLM returns. IDs are
// strings (memory.Store docIDs, typically "mem-<sha256>" from
// dedup.DocID) and every action carries the source scope so the
// handler can call DeleteDocument / Index against the right scope
// without a lookup pass.
type memoryConsolidationAction struct {
	Action     string   `json:"action"`                // "merge", "delete", "retype"
	Scope      string   `json:"scope"`                 // "fact" | "preference" | "decision" | "contact"
	IDs        []string `json:"ids"`                   // memory.Store doc IDs
	MergedText string   `json:"merged_text,omitempty"` // for merge
	NewScope   string   `json:"new_scope,omitempty"`   // for retype (use scope-aware field name)
	Reason     string   `json:"reason"`
}

// RunOnce performs one consolidation cycle:
// 1. If the extractor has pending changes, extract first.
// 2. Then consolidate the memory store (dedup, merge, retype, prune).
func (c *Consolidator) RunOnce() error {
	// Step 1: fallback extraction for unprocessed changes.
	if c.extractor.HasChanges() {
		log.Printf("memstore/consolidator: pending git changes detected, running extraction first")
		if err := c.extractor.Extract(); err != nil {
			log.Printf("memstore/consolidator: fallback extraction failed: %v", err)
			// Continue to consolidation anyway.
		}
	}

	// Step 2: consolidate. Pick the backend snapshot once per run so a
	// concurrent SetMemoryBackend call doesn't flip mid-cycle.
	c.mu.Lock()
	memStore := c.memStore
	memScopes := append([]memory.Scope(nil), c.memScopes...)
	threshold := c.nearDupThreshold
	c.mu.Unlock()

	if memStore == nil || len(memScopes) == 0 {
		log.Printf("memstore/consolidator: no memory backend configured, skipping")
		return nil
	}
	return c.consolidateMemory(memStore, memScopes, threshold)
}

// consolidateMemory is the memory.Store-backed consolidation path. Walks
// every configured scope via ListDocuments, asks the LLM for merge /
// delete / retype actions in a single prompt, and applies them via
// dedup.IndexWithDedup + DeleteDocument.
func (c *Consolidator) consolidateMemory(store memory.Store, scopes []memory.Scope, threshold float32) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Walk every scope and build a (doc, scope) index table. The LLM
	// sees string IDs scoped by their source — no per-action lookup is
	// needed later because every action carries its scope.
	type scoped struct {
		doc   memory.Document
		scope memory.Scope
	}
	var all []scoped
	for _, sc := range scopes {
		docs, err := store.ListDocuments(ctx, sc, 500)
		if err != nil {
			log.Printf("memstore/consolidator: list scope=%q: %v", sc, err)
			continue
		}
		for _, d := range docs {
			all = append(all, scoped{doc: d, scope: sc})
		}
	}
	if len(all) < 5 {
		log.Printf("memstore/consolidator: only %d memories across %d scopes, skipping", len(all), len(scopes))
		return nil
	}

	var sb strings.Builder
	for _, it := range all {
		createdAt := it.doc.Metadata["created_at"]
		if createdAt == "" {
			createdAt = "unknown"
		}
		sb.WriteString(fmt.Sprintf("[ID:%s] [%s] [%s] %s\n", it.doc.ID, it.scope, createdAt, it.doc.Text))
	}
	log.Printf("memstore/consolidator: consolidating %d memories across %d scopes (%d bytes)", len(all), len(scopes), sb.Len())

	actions, err := c.identifyMemoryActions(sb.String())
	if err != nil {
		return fmt.Errorf("identify memory actions: %w", err)
	}
	if len(actions) == 0 {
		log.Printf("memstore/consolidator: no consolidation needed")
		return nil
	}

	applied := 0
	for _, a := range actions {
		switch a.Action {
		case "merge":
			if len(a.IDs) < 2 || strings.TrimSpace(a.MergedText) == "" || a.Scope == "" {
				continue
			}
			destScope := a.Scope
			if a.NewScope != "" {
				destScope = a.NewScope
			}
			// Write the merged fact first — if that fails we keep the
			// originals untouched.
			res, err := dedup.IndexWithDedup(ctx, store, memory.Scope(destScope), memory.Document{Text: a.MergedText}, dedup.Options{
				Source:           "consolidator",
				NearDupThreshold: threshold,
				Now:              func() string { return time.Now().Format(time.RFC3339) },
			})
			if err != nil {
				log.Printf("memstore/consolidator: merge write failed: %v", err)
				continue
			}
			for _, id := range a.IDs {
				if _, delErr := store.DeleteDocument(ctx, memory.Scope(a.Scope), id); delErr != nil {
					log.Printf("memstore/consolidator: merge delete %s/%s failed: %v", a.Scope, id, delErr)
				}
			}
			log.Printf("memstore/consolidator: merged %v → %s (scope=%s, stored=%v)", a.IDs, res.DocID, destScope, res.Stored)
			applied++

		case "delete":
			if a.Scope == "" {
				continue
			}
			for _, id := range a.IDs {
				if _, err := store.DeleteDocument(ctx, memory.Scope(a.Scope), id); err != nil {
					log.Printf("memstore/consolidator: delete %s/%s failed: %v", a.Scope, id, err)
				}
			}
			applied++

		case "retype":
			if len(a.IDs) == 0 || a.NewScope == "" || a.Scope == "" {
				continue
			}
			for _, id := range a.IDs {
				existing, err := store.GetDocument(ctx, memory.Scope(a.Scope), id)
				if err != nil || existing == nil {
					continue
				}
				if _, err := store.DeleteDocument(ctx, memory.Scope(a.Scope), id); err != nil {
					log.Printf("memstore/consolidator: retype delete %s/%s failed: %v", a.Scope, id, err)
					continue
				}
				if _, err := dedup.IndexWithDedup(ctx, store, memory.Scope(a.NewScope), memory.Document{Text: existing.Text}, dedup.Options{
					Source:           "consolidator",
					NearDupThreshold: threshold,
					Now:              func() string { return time.Now().Format(time.RFC3339) },
				}); err != nil {
					log.Printf("memstore/consolidator: retype write %s/%s → %s failed: %v", a.Scope, id, a.NewScope, err)
				}
			}
			applied++
		}
	}

	log.Printf("memstore/consolidator: applied %d/%d actions", applied, len(actions))
	return nil
}

// identifyMemoryActions is the memory.Store-backed twin of
// identifyActions. The prompt asks for scoped actions with string IDs
// so the LLM response maps straight onto memory.Store operations
// without a second lookup pass.
func (c *Consolidator) identifyMemoryActions(memoryList string) ([]memoryConsolidationAction, error) {
	prompt := fmt.Sprintf(memoryConsolidationPrompt, memoryList)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	var model string
	if c.extractor.tierResolver != nil {
		model = c.extractor.tierResolver()
	}
	if model == "" {
		return nil, fmt.Errorf("no tier available for memory consolidation (tierResolver returned empty)")
	}

	raw, err := c.provider.Invoke(ctx, prompt, ExtractorParams{
		Model:    model,
		MaxTurns: 1,
		DataDir:  c.extractor.dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM consolidation: %w", err)
	}

	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var actions []memoryConsolidationAction
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		if start := strings.Index(raw, "["); start != -1 {
			if end := strings.LastIndex(raw, "]"); end > start {
				if err2 := json.Unmarshal([]byte(raw[start:end+1]), &actions); err2 == nil {
					return actions, nil
				}
			}
		}
		return nil, fmt.Errorf("parse consolidation response: %w (raw: %s)", err, truncateText(raw, 200))
	}
	return actions, nil
}

const memoryConsolidationPrompt = `You are a memory consolidation tool. Review the memory store and identify cleanup actions.

Rules:
- MERGE memories that say the same thing differently (keep the most complete version). Requires: scope, ids (>=2), merged_text, new_scope (usually same as scope).
- DELETE memories that are clearly outdated, contradicted by newer entries, or trivially obvious. Requires: scope, ids.
- RETYPE memories whose scope is wrong (e.g., a "fact" that is actually a "preference" or "decision"). Requires: scope (current), ids, new_scope (correct).
- Do NOT delete memories that are still relevant even if old.
- Do NOT merge memories that are about different topics even if related.
- Be conservative — when in doubt, keep the memory.

Valid scopes: "fact", "preference", "decision", "contact"

Respond with ONLY a JSON array of actions:
[{"action": "merge|delete|retype", "scope": "fact", "ids": ["mem-abc", "mem-def"], "merged_text": "... (merge only)", "new_scope": "... (merge/retype only)", "reason": "brief explanation"}]

If no actions needed, respond with: []

<memory_store>
%s
</memory_store>`

