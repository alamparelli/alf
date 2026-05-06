package pending

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var fixedTimeDir = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func newDirStore(t *testing.T) *DirStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pending")
	store, err := NewDirStore(dir, func() time.Time { return fixedTimeDir })
	if err != nil {
		t.Fatalf("NewDirStore: %v", err)
	}
	return store
}

func TestDirStore_RoundTrip(t *testing.T) {
	store := newDirStore(t)
	ctx := context.Background()

	id1, err := store.Append(ctx, Item{Kind: KindTrustAdd, Payload: map[string]string{"fp": "abc"}})
	if err != nil {
		t.Fatalf("Append1: %v", err)
	}
	id2, err := store.Append(ctx, Item{Kind: KindBundleInstall, Payload: map[string]string{"path": "/tmp/x"}})
	if err != nil {
		t.Fatalf("Append2: %v", err)
	}
	if id1 == id2 {
		t.Errorf("ids must differ: %s == %s", id1, id2)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("List len: got %d, want 2", len(items))
	}
	if items[0].ID != id1 || items[1].ID != id2 {
		t.Errorf("oldest-first ordering broken: %v", []string{items[0].ID, items[1].ID})
	}

	approved, err := store.Approve(ctx, id1)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Kind != KindTrustAdd {
		t.Errorf("Approved item kind: got %q, want %q", approved.Kind, KindTrustAdd)
	}
	if approved.Payload["fp"] != "abc" {
		t.Errorf("payload lost: %v", approved.Payload)
	}

	items, _ = store.List(ctx)
	if len(items) != 1 || items[0].ID != id2 {
		t.Errorf("after Approve: items=%v", items)
	}

	if _, err := store.Approve(ctx, id1); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Approve: got %v, want ErrNotFound", err)
	}
}

// TestDirStore_PersistAcrossRestarts pins the chunk-3 reason for
// existing: append items, drop the *DirStore, open a new one against
// the same dir, the items are still there. memoryStore would lose
// them on restart.
func TestDirStore_PersistAcrossRestarts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	store1, err := NewDirStore(dir, func() time.Time { return fixedTimeDir })
	if err != nil {
		t.Fatalf("NewDirStore #1: %v", err)
	}
	id, err := store1.Append(context.Background(), Item{Kind: KindPermissionWiden, Payload: map[string]string{"diff": "+http"}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Drop the in-memory state and re-open the same dir.
	store2, err := NewDirStore(dir, func() time.Time { return fixedTimeDir })
	if err != nil {
		t.Fatalf("NewDirStore #2: %v", err)
	}
	items, err := store2.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != id {
		t.Errorf("post-restart list: got %v, want [%s]", items, id)
	}

	// Next Append must allocate an id strictly greater than the
	// recovered max — no collision.
	id2, err := store2.Append(context.Background(), Item{Kind: KindTrustAdd})
	if err != nil {
		t.Fatalf("Append #2: %v", err)
	}
	if id2 <= id {
		t.Errorf("post-restart Append id %s must be > %s", id2, id)
	}
}

func TestDirStore_DenyRoundTrip(t *testing.T) {
	store := newDirStore(t)
	ctx := context.Background()
	id, _ := store.Append(ctx, Item{Kind: KindTrustAdd})

	denied, err := store.Deny(ctx, id)
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if denied.ID != id {
		t.Errorf("Deny returned wrong item: got %s, want %s", denied.ID, id)
	}
	items, _ := store.List(ctx)
	if len(items) != 0 {
		t.Errorf("after Deny: items=%v", items)
	}
}

func TestDirStore_AppendIgnoresCallerSuppliedID(t *testing.T) {
	// Same invariant memoryStore enforces: agent-controlled IDs cannot
	// drive collision attacks. The Store assigns the id; whatever the
	// caller put in item.ID is overwritten.
	store := newDirStore(t)
	id, err := store.Append(context.Background(), Item{ID: "../../etc/passwd", Kind: KindTrustAdd})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id == "../../etc/passwd" {
		t.Errorf("Store accepted caller-supplied id: %s", id)
	}
	// Filesystem-level proof: <dir>/etc/passwd does not exist (the id
	// was not used as a path component).
	bogus := filepath.Join(filepath.Dir(filepath.Dir(store.Dir())), "etc", "passwd")
	if _, err := os.Stat(bogus); err == nil {
		t.Errorf("path traversal landed: %s exists", bogus)
	}
}

func TestDirStore_RefusesPermissiveDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := NewDirStore(dir, nil)
	if err == nil || !contains(err.Error(), "permissive perms") {
		t.Fatalf("got %v, want permissive-perms refusal", err)
	}
}

func TestDirStore_Concurrency(t *testing.T) {
	store := newDirStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := store.Append(ctx, Item{Kind: KindTrustAdd, Payload: map[string]string{"i": fmt.Sprintf("%d", i)}})
			if err != nil {
				t.Errorf("Append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	items, _ := store.List(ctx)
	if len(items) != n {
		t.Errorf("Concurrency: got %d items, want %d", len(items), n)
	}
	// All ids must be unique.
	seen := make(map[string]struct{})
	for _, it := range items {
		if _, ok := seen[it.ID]; ok {
			t.Errorf("Concurrency: duplicate id %s", it.ID)
		}
		seen[it.ID] = struct{}{}
	}
}

func TestDirStore_ItemFilePerms(t *testing.T) {
	store := newDirStore(t)
	id, _ := store.Append(context.Background(), Item{Kind: KindTrustAdd})
	info, err := os.Stat(filepath.Join(store.Dir(), id+".json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("item file perms: got %v, want 0600", mode)
	}
	dinfo, err := os.Stat(store.Dir())
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if mode := dinfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir perms: got %v, want 0700", mode)
	}
}

func TestDirStore_EmptyList(t *testing.T) {
	store := newDirStore(t)
	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("fresh store should be empty, got %v", items)
	}
}

func TestDefaultDir(t *testing.T) {
	got := DefaultDir("/var/lib/alf/data")
	want := "/var/lib/alf/data/admin/pending"
	if got != want {
		t.Errorf("DefaultDir: got %q, want %q", got, want)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
