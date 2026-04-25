package pending

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestMemoryStore_AppendAssignsIDAndTimestamp(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore(fixedClock(now))

	id, err := s.Append(context.Background(), Item{
		Kind:    KindTrustAdd,
		Payload: map[string]string{"fingerprint": "abc"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id == "" {
		t.Fatal("Append returned empty ID")
	}

	items, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("List len=%d", len(items))
	}
	if items[0].ID != id {
		t.Errorf("List[0].ID=%q, want %q", items[0].ID, id)
	}
	if !items[0].CreatedAt.Equal(now) {
		t.Errorf("CreatedAt=%v, want %v", items[0].CreatedAt, now)
	}
}

func TestMemoryStore_AppendIgnoresCallerID(t *testing.T) {
	// Caller-supplied IDs must be overwritten — the store assigns its
	// own. This blocks an agent from forging predictable IDs.
	s := NewMemoryStore(nil)
	id, err := s.Append(context.Background(), Item{
		ID:   "agent-controlled-id",
		Kind: KindTrustAdd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "agent-controlled-id" {
		t.Errorf("Store accepted caller-controlled ID; got %q", id)
	}
}

func TestMemoryStore_ListIsOldestFirst(t *testing.T) {
	t1 := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore(nil)

	_, _ = s.Append(context.Background(), Item{Kind: KindTrustAdd, CreatedAt: t2})
	_, _ = s.Append(context.Background(), Item{Kind: KindBundleInstall, CreatedAt: t1})

	items, _ := s.List(context.Background())
	if len(items) != 2 {
		t.Fatalf("len=%d", len(items))
	}
	if !items[0].CreatedAt.Equal(t1) {
		t.Errorf("oldest-first violated: items[0].CreatedAt=%v, want %v", items[0].CreatedAt, t1)
	}
}

func TestMemoryStore_ApproveRemoves(t *testing.T) {
	s := NewMemoryStore(nil)
	id, _ := s.Append(context.Background(), Item{Kind: KindTrustAdd})

	got, err := s.Approve(context.Background(), id)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.ID != id {
		t.Errorf("Approve returned ID=%q, want %q", got.ID, id)
	}

	items, _ := s.List(context.Background())
	if len(items) != 0 {
		t.Errorf("approved item still listed: %v", items)
	}
}

func TestMemoryStore_DenyRemoves(t *testing.T) {
	s := NewMemoryStore(nil)
	id, _ := s.Append(context.Background(), Item{Kind: KindTrustAdd})

	if _, err := s.Deny(context.Background(), id); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	items, _ := s.List(context.Background())
	if len(items) != 0 {
		t.Errorf("denied item still listed: %v", items)
	}
}

func TestMemoryStore_ApproveUnknownReturnsErrNotFound(t *testing.T) {
	s := NewMemoryStore(nil)
	_, err := s.Approve(context.Background(), "no-such-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_PreservesPayloadAndCreatedBy(t *testing.T) {
	s := NewMemoryStore(nil)
	owner := capability.ID("some-cap")
	id, _ := s.Append(context.Background(), Item{
		Kind:      KindBundleInstall,
		Payload:   map[string]string{"path": "/tmp/x.wasm", "fingerprint": "deadbeef"},
		CreatedBy: owner,
	})

	items, _ := s.List(context.Background())
	if len(items) != 1 {
		t.Fatal("missing item")
	}
	got := items[0]
	if got.ID != id {
		t.Errorf("ID=%q", got.ID)
	}
	if got.Kind != KindBundleInstall {
		t.Errorf("Kind=%q", got.Kind)
	}
	if got.Payload["path"] != "/tmp/x.wasm" || got.Payload["fingerprint"] != "deadbeef" {
		t.Errorf("Payload=%v", got.Payload)
	}
	if got.CreatedBy != owner {
		t.Errorf("CreatedBy=%q, want %q", got.CreatedBy, owner)
	}
}
