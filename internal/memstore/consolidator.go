package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// Consolidator periodically deduplicates and reorganizes the memory store.
// It also acts as a fallback extractor for periods with no user sessions
// (e.g., overnight scheduled jobs).
type Consolidator struct {
	store     *Store
	extractor *Extractor
	provider  ExtractorProvider
	timeout   time.Duration
}

// NewConsolidator creates a new consolidator.
func NewConsolidator(store *Store, extractor *Extractor, prov ExtractorProvider, timeout time.Duration) *Consolidator {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &Consolidator{
		store:     store,
		extractor: extractor,
		provider:  prov,
		timeout:   timeout,
	}
}

// consolidationAction represents a single action the LLM wants to perform.
type consolidationAction struct {
	Action    string `json:"action"`     // "merge", "delete", "retype"
	IDs       []int64 `json:"ids"`       // memory IDs involved
	MergedText string `json:"merged_text,omitempty"` // for merge: the unified text
	NewType   string  `json:"new_type,omitempty"`    // for retype: the correct type
	Reason    string  `json:"reason"`
}

// RunOnce performs one consolidation cycle:
// 1. If the extractor has pending changes, extract first.
// 2. Then consolidate the memstore (dedup, merge, retype, prune).
func (c *Consolidator) RunOnce() error {
	// Step 1: fallback extraction for unprocessed changes.
	if c.extractor.HasChanges() {
		log.Printf("memstore/consolidator: pending git changes detected, running extraction first")
		if err := c.extractor.Extract(); err != nil {
			log.Printf("memstore/consolidator: fallback extraction failed: %v", err)
			// Continue to consolidation anyway.
		}
	}

	// Step 2: consolidate.
	return c.consolidate()
}

func (c *Consolidator) consolidate() error {
	// Fetch all memories.
	memories, err := c.store.Recent(365, 500) // last year, max 500
	if err != nil {
		return fmt.Errorf("fetch memories: %w", err)
	}

	if len(memories) < 5 {
		log.Printf("memstore/consolidator: only %d memories, skipping consolidation", len(memories))
		return nil
	}

	// Build memory list for the LLM.
	var sb strings.Builder
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("[ID:%d] [%s] [%s] %s\n", m.ID, m.Type, m.CreatedAt.Format("2006-01-02"), m.Text))
	}
	memoryList := sb.String()

	log.Printf("memstore/consolidator: consolidating %d memories (%d bytes)", len(memories), len(memoryList))

	// Ask LLM to identify consolidation actions.
	actions, err := c.identifyActions(memoryList)
	if err != nil {
		return fmt.Errorf("identify actions: %w", err)
	}

	if len(actions) == 0 {
		log.Printf("memstore/consolidator: no consolidation needed")
		return nil
	}

	// Apply actions.
	applied := 0
	for _, action := range actions {
		switch action.Action {
		case "merge":
			if len(action.IDs) < 2 || action.MergedText == "" {
				continue
			}
			// Store merged fact, then delete originals.
			memType := "fact"
			if action.NewType != "" {
				memType = action.NewType
			}
			newID, err := c.store.Store(action.MergedText, memType, "consolidator", nil)
			if err != nil {
				log.Printf("memstore/consolidator: merge store failed: %v", err)
				continue
			}
			log.Printf("memstore/consolidator: merged %v → id=%d", action.IDs, newID)
			for _, id := range action.IDs {
				c.store.Delete(id)
			}
			applied++

		case "delete":
			for _, id := range action.IDs {
				c.store.Delete(id)
			}
			applied++

		case "retype":
			if len(action.IDs) == 0 || action.NewType == "" {
				continue
			}
			// Delete and re-store with correct type.
			for _, id := range action.IDs {
				// Find the memory text.
				for _, m := range memories {
					if m.ID == id {
						c.store.Delete(id)
						c.store.Store(m.Text, action.NewType, "consolidator", nil)
						break
					}
				}
			}
			applied++
		}
	}

	log.Printf("memstore/consolidator: applied %d/%d actions", applied, len(actions))
	return nil
}

const consolidationPrompt = `You are a memory consolidation tool. Review the memory store and identify cleanup actions.

Rules:
- MERGE memories that say the same thing differently (keep the most complete version)
- DELETE memories that are clearly outdated, contradicted by newer entries, or trivially obvious
- RETYPE memories whose type is wrong (e.g., a "fact" that is actually a "preference" or "decision")
- Do NOT delete memories that are still relevant even if old
- Do NOT merge memories that are about different topics even if related
- Be conservative — when in doubt, keep the memory

Valid types: "fact", "preference", "decision", "contact"

Respond with ONLY a JSON array of actions:
[{"action": "merge|delete|retype", "ids": [1, 2], "merged_text": "unified text (merge only)", "new_type": "correct type (merge/retype)", "reason": "brief explanation"}]

If no actions needed, respond with: []

<memory_store>
%s
</memory_store>`

func (c *Consolidator) identifyActions(memoryList string) ([]consolidationAction, error) {
	prompt := fmt.Sprintf(consolidationPrompt, memoryList)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Resolve model from the configured tier — never hardcode (#291).
	// Users may run any backend; a baked-in Claude model would bypass config.
	var model string
	if c.extractor.tierResolver != nil {
		model = c.extractor.tierResolver()
	}
	if model == "" {
		return nil, fmt.Errorf("no tier available for memory consolidation (tierResolver returned empty)")
	}
	log.Printf("memstore/consolidator: invoking with model=%s", model)

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

	var actions []consolidationAction
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
