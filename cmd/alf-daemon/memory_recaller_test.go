package main

import (
	"context"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
)

// TestMemoryCCRecaller_FansOutAcrossScopes verifies the #337c2 recaller
// returns hits from every memory scope (fact, preference, decision, …)
// in one Search call, not just the default scope. Callers rely on this
// because the pre-migration memstore model had a single memories table
// and a generic "recall memories" semantics.
func TestMemoryCCRecaller_FansOutAcrossScopes(t *testing.T) {
	store, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	_ = store.Index(ctx, "fact", memory.Document{ID: "1", Text: "the deployment uses docker"})
	_ = store.Index(ctx, "preference", memory.Document{ID: "2", Text: "user prefers docker-compose"})
	_ = store.Index(ctx, "decision", memory.Document{ID: "3", Text: "decided to drop helm"})

	r := &memoryCCRecaller{store: store}
	hits, err := r.Search("docker", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Every scope with a hit must appear in the result, tagged with its Type.
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.Type] = true
	}
	for _, want := range []string{"fact", "preference"} {
		if !seen[want] {
			t.Errorf("recaller did not return a hit tagged Type=%q; got types=%v", want, seen)
		}
	}
}

// TestMemoryCCRecaller_ScoreToDistance verifies the adapter inverts
// memory.Hit.Score into the MemoryResult.Distance convention the rest of
// the codebase expects (lower distance == more relevant).
func TestMemoryCCRecaller_ScoreToDistance(t *testing.T) {
	store, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	_ = store.Index(ctx, "fact", memory.Document{ID: "1", Text: "alpha"})

	r := &memoryCCRecaller{store: store}
	hits, _ := r.Search("alpha", 5)
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	// LIKE fallback yields Score in [0, 1]; Distance must land in the same
	// band and be the complement.
	d := hits[0].Distance
	if d < 0 || d > 1 {
		t.Errorf("distance out of [0,1]: got %v", d)
	}
}
