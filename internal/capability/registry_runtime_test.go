package capability

import (
	"testing"
)

// Resolve mirrors Get — same hit / miss semantics.
func TestRegistry_Resolve_MatchesGet(t *testing.T) {
	r := NewRegistry()
	c := newStub("bash", KindTool)
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := r.Resolve("bash")
	if !ok {
		t.Fatal("Resolve: expected hit for registered capability")
	}
	if got.Manifest().ID != "bash" {
		t.Fatalf("Resolve: returned wrong capability: %+v", got.Manifest())
	}

	if _, ok := r.Resolve("missing"); ok {
		t.Fatal("Resolve: expected miss for unregistered id")
	}
}

// List returns one Manifest per registered Capability, sorted by ID.
func TestRegistry_List_ReturnsSortedManifests(t *testing.T) {
	r := NewRegistry()
	for _, id := range []ID{"grep", "bash", "read_file"} {
		if err := r.Register(newStub(id, KindTool)); err != nil {
			t.Fatalf("Register %q: %v", id, err)
		}
	}

	mans := r.List()
	if len(mans) != 3 {
		t.Fatalf("List len: got %d want 3", len(mans))
	}
	wantOrder := []ID{"bash", "grep", "read_file"}
	for i, want := range wantOrder {
		if mans[i].ID != want {
			t.Fatalf("List[%d].ID: got %q want %q", i, mans[i].ID, want)
		}
	}
}

// List on an empty registry must be a non-nil empty slice — callers build
// []ai.ToolSpec by ranging over it and a nil slice is fine, but the allocation
// contract matters for the Runtime code path that caches it.
func TestRegistry_List_EmptyRegistry(t *testing.T) {
	r := NewRegistry()
	mans := r.List()
	if mans == nil {
		t.Fatal("List on empty Registry returned nil; want empty slice")
	}
	if len(mans) != 0 {
		t.Fatalf("List on empty Registry: got %d entries, want 0", len(mans))
	}
}
