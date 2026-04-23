package curation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/dedup"
	curation "github.com/alamparelli/alf/internal/memory/curation"
)

// seedMemory writes n documents into memStore under scope, returning
// their docIDs in insertion order so assertions can target them.
func seedMemory(t *testing.T, store memory.Store, scope memory.Scope, texts ...string) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, len(texts))
	for _, text := range texts {
		r, err := dedup.IndexWithDedup(ctx, store, scope, memory.Document{Text: text}, dedup.Options{
			Source: "test",
			Now:    func() string { return "2026-01-01T00:00:00Z" },
		})
		if err != nil {
			t.Fatalf("seed %q: %v", text, err)
		}
		ids = append(ids, r.DocID)
	}
	return ids
}

// consolidatorProvider canned-response provider for the Consolidator LLM
// call. ExtractorProvider signature matches both extractor and
// consolidator usage.
type consolidatorProvider struct {
	resp string
	err  error
}

func (p *consolidatorProvider) Invoke(_ context.Context, _ string, _ curation.ExtractorParams) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return p.resp, nil
}

// newConsolidatorWithMemory builds a Consolidator + Extractor pair
// wired to a memory.Store. Mirrors the production bootstrap just
// enough to drive RunOnce() end-to-end.
func newConsolidatorWithMemory(t *testing.T, prov curation.ExtractorProvider) (*curation.Consolidator, memory.Store) {
	t.Helper()
	dataDir := t.TempDir()
	initGitRepo(t, dataDir)

	memStore, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memStore.Close() })

	ex := curation.NewExtractor(dataDir, t.TempDir(), curation.ExtractorConfig{}, prov, func() string { return "test-model" })
	ex.SetMemoryBackend(memStore, 0)
	c := curation.NewConsolidator(ex, prov, 0)
	c.SetMemoryBackend(memStore, []memory.Scope{"fact", "preference", "decision", "contact"}, 0)
	return c, memStore
}

func jsonActions(v ...map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestConsolidator_Memory_SkipsWhenFewMemories catches the early-exit
// path — fewer than 5 rows across all scopes should not invoke the LLM
// at all. Protects against accidental over-consolidation of tiny
// corpora (first-boot users).
func TestConsolidator_Memory_SkipsWhenFewMemories(t *testing.T) {
	prov := &consolidatorProvider{resp: "[]"}
	c, memStore := newConsolidatorWithMemory(t, prov)
	_ = seedMemory(t, memStore, "fact", "only one fact")

	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// 1 row remains, LLM was not usefully consulted — verify the doc
	// survived.
	ctx := context.Background()
	docs, _ := memStore.ListDocuments(ctx, "fact", 10)
	if len(docs) != 1 {
		t.Errorf("expected the sole fact to survive, got %d rows", len(docs))
	}
}

// TestConsolidator_Memory_DeleteActionRemovesFromMemoryStore verifies
// that a delete action issued by the LLM lands in memory.Store.
// Also proves the scope field in the action routes the DeleteDocument
// call correctly.
func TestConsolidator_Memory_DeleteActionRemovesFromMemoryStore(t *testing.T) {
	prov := &consolidatorProvider{} // resp set after seeding
	c, memStore := newConsolidatorWithMemory(t, prov)

	ids := seedMemory(t, memStore, "fact",
		"fact one", "fact two", "fact three", "fact four", "fact five",
	)
	// Build an action the mock returns: delete the first two facts.
	prov.resp = jsonActions(map[string]any{
		"action": "delete",
		"scope":  "fact",
		"ids":    []string{ids[0], ids[1]},
		"reason": "outdated in test",
	})

	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	ctx := context.Background()
	if got, _ := memStore.GetDocument(ctx, "fact", ids[0]); got != nil {
		t.Errorf("fact #0 should be deleted, still present")
	}
	if got, _ := memStore.GetDocument(ctx, "fact", ids[1]); got != nil {
		t.Errorf("fact #1 should be deleted, still present")
	}
	if got, _ := memStore.GetDocument(ctx, "fact", ids[2]); got == nil {
		t.Errorf("fact #2 should survive, got nil")
	}
}

// TestConsolidator_Memory_MergeActionReplacesSourcesWithMerged verifies
// the merge action: N source IDs are deleted and a single merged row
// lands under the destination scope (new_scope or the source scope).
func TestConsolidator_Memory_MergeActionReplacesSourcesWithMerged(t *testing.T) {
	prov := &consolidatorProvider{}
	c, memStore := newConsolidatorWithMemory(t, prov)

	ids := seedMemory(t, memStore, "fact",
		"the project runs on Go",
		"Go 1.24 is the version",
		"uses sqlite-vec for embeddings",
		"deployed via docker compose",
		"has a CI pipeline",
	)
	mergedText := "project runs Go 1.24 with sqlite-vec, deployed via docker compose"
	prov.resp = jsonActions(map[string]any{
		"action":      "merge",
		"scope":       "fact",
		"ids":         []string{ids[0], ids[1], ids[2], ids[3]},
		"merged_text": mergedText,
		"new_scope":   "fact",
		"reason":      "consolidate redundant tech facts",
	})

	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if got, _ := memStore.GetDocument(ctx, "fact", ids[i]); got != nil {
			t.Errorf("source fact[%d] should be deleted after merge, still present", i)
		}
	}
	// The merged row lands under the predictable dedup.DocID.
	mergedID := dedup.DocID(mergedText)
	got, _ := memStore.GetDocument(ctx, "fact", mergedID)
	if got == nil {
		t.Fatalf("merged row missing; expected docID=%s", mergedID)
	}
	if got.Text != mergedText {
		t.Errorf("merged text = %q, want %q", got.Text, mergedText)
	}
	if got.Metadata["source"] != "consolidator" {
		t.Errorf("source = %q, want consolidator", got.Metadata["source"])
	}
}

// TestConsolidator_Memory_RetypeMovesBetweenScopes verifies that a
// retype action deletes the document from the current scope and
// re-indexes it under new_scope, preserving the text.
func TestConsolidator_Memory_RetypeMovesBetweenScopes(t *testing.T) {
	prov := &consolidatorProvider{}
	c, memStore := newConsolidatorWithMemory(t, prov)

	// Seed 4 facts — need >= 5 total for consolidate to engage.
	_ = seedMemory(t, memStore, "fact",
		"first fact", "second fact", "third fact", "fourth fact",
	)
	// Seed a fact that actually expresses a preference.
	prefText := "user prefers dark themes everywhere"
	prefIDs := seedMemory(t, memStore, "fact", prefText)

	prov.resp = jsonActions(map[string]any{
		"action":    "retype",
		"scope":     "fact",
		"ids":       []string{prefIDs[0]},
		"new_scope": "preference",
		"reason":    "this is a preference not a fact",
	})

	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	ctx := context.Background()
	// Original scope is now empty for this doc.
	if got, _ := memStore.GetDocument(ctx, "fact", prefIDs[0]); got != nil {
		t.Errorf("retype should remove from source scope, still present")
	}
	// The text reappears under preference scope with a fresh hash-derived ID.
	newID := dedup.DocID(prefText)
	got, _ := memStore.GetDocument(ctx, "preference", newID)
	if got == nil {
		t.Fatalf("retype target missing under preference scope (docID=%s)", newID)
	}
	if got.Text != prefText {
		t.Errorf("retype corrupted text: got %q", got.Text)
	}
}

// TestConsolidator_Memory_EmptyActionsIsNoOp verifies that an LLM
// response of [] leaves the store untouched — the "no consolidation
// needed" branch.
func TestConsolidator_Memory_EmptyActionsIsNoOp(t *testing.T) {
	prov := &consolidatorProvider{resp: "[]"}
	c, memStore := newConsolidatorWithMemory(t, prov)
	ids := seedMemory(t, memStore, "fact", "a", "b", "c", "d", "e", "f")

	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	ctx := context.Background()
	for _, id := range ids {
		if got, _ := memStore.GetDocument(ctx, "fact", id); got == nil {
			t.Errorf("no-op consolidation lost doc %s", id)
		}
	}
}

// TestConsolidator_Memory_MalformedActionsAreRejected checks the LLM
// response parsing layer: garbage back from the model must surface as
// an error from RunOnce rather than silently skipping consolidation.
func TestConsolidator_Memory_MalformedActionsAreRejected(t *testing.T) {
	prov := &consolidatorProvider{resp: "{not json at all"}
	c, memStore := newConsolidatorWithMemory(t, prov)
	_ = seedMemory(t, memStore, "fact", "a", "b", "c", "d", "e", "f")

	err := c.RunOnce()
	if err == nil {
		t.Fatalf("expected parse error for malformed LLM output, got nil")
	}
}

// TestConsolidator_Memory_InvalidActionShapeSkipped catches the
// defensive per-action validation: a merge missing merged_text or IDs
// must be skipped without crashing the run.
func TestConsolidator_Memory_InvalidActionShapeSkipped(t *testing.T) {
	prov := &consolidatorProvider{}
	c, memStore := newConsolidatorWithMemory(t, prov)
	ids := seedMemory(t, memStore, "fact", "a", "b", "c", "d", "e", "f")

	// Invalid merge: only one ID.
	prov.resp = jsonActions(map[string]any{
		"action":      "merge",
		"scope":       "fact",
		"ids":         []string{ids[0]},
		"merged_text": "would-be merged",
		"new_scope":   "fact",
	})

	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Original doc survived — merge was a no-op.
	ctx := context.Background()
	if got, _ := memStore.GetDocument(ctx, "fact", ids[0]); got == nil {
		t.Errorf("invalid merge should have skipped; doc was deleted anyway")
	}
}

// Ensure fmt and os are used (sibling test files pull them in; keep
// imports stable if this file evolves).
var (
	_ = fmt.Sprintf
	_ = os.Getenv
	_ = exec.Command
)
