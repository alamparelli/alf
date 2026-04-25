// Package pending hosts the admin-side ratification queue. Items in
// this queue represent operations the agent prepared on the user's
// behalf that require human approval before they take effect: a new
// trust-store entry, a permission widening, an `alf install` of a
// bundle signed by an unknown key.
//
// Per ARCHITECTURE-SECURITY §6 + #395, the queue is reachable only
// from the admin trust domain — TTY-direct CLI commands (`alf pending`,
// `alf ratify`) and the CC /admin/ratify page (Stage 2). Nothing in
// internal/capability, internal/runtime, or internal/tooling may
// import this package; the archtest TestAdminPackageBoundary fails
// the build if a violation slips in.
//
// Stage 1 ships an in-memory Store implementation. Stage 2 will add a
// SQLite-backed Store written to a path that no capability handle can
// reach (the archtest will then also pin the path).
package pending

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// Kind enumerates the operation classes the queue accepts. Adding a
// new kind is a deliberate schema change — every consumer of the
// queue switches on this enum, so a typo here surfaces as a missing
// case at every call site.
type Kind string

const (
	// KindTrustAdd represents a request to add a public key to the
	// trust store. Payload carries the pubkey + a one-line rationale.
	KindTrustAdd Kind = "trust.add"

	// KindBundleInstall represents a bundle install whose signing key
	// is not in the trust store. Payload carries the bundle path +
	// the signer fingerprint.
	KindBundleInstall Kind = "bundle.install"

	// KindPermissionWiden represents a manifest update that widens
	// the cap's existing permissions. Payload carries the diff.
	KindPermissionWiden Kind = "permission.widen"
)

// Item is one entry in the queue. It is intentionally narrow — the
// queue does not host arbitrary blobs the agent can stuff with
// LLM-controlled data; every Kind has a fixed payload shape the
// admin tooling knows how to read.
type Item struct {
	ID         string            // ULID-like; opaque to consumers
	Kind       Kind              // dispatch tag
	Payload    map[string]string // narrow string-keyed metadata
	CreatedBy  capability.ID     // which capability prepared the item ("" = daemon)
	CreatedAt  time.Time
}

// Store is the queue contract. The in-memory implementation suffices
// for Stage 1; the production daemon swaps in a SQLite-backed Store
// when Stage 2 lands. Any consumer takes Store, not the concrete
// type, so the swap is one wiring line.
type Store interface {
	// Append adds an item to the queue. The Store assigns the ID
	// (caller-supplied IDs are ignored to prevent agent-controlled
	// collision attacks). Returns the assigned ID + error.
	Append(ctx context.Context, item Item) (string, error)

	// List returns all pending items, oldest-first. The slice is a
	// copy — the Store's internal state is not exposed to mutation.
	List(ctx context.Context) ([]Item, error)

	// Approve removes the item with the given ID, signalling the
	// caller that the operation may proceed. Returns ErrNotFound if
	// the id is unknown.
	Approve(ctx context.Context, id string) (Item, error)

	// Deny removes the item with the given ID without further
	// action. Returns ErrNotFound if the id is unknown.
	Deny(ctx context.Context, id string) (Item, error)
}

// ErrNotFound is returned by Approve/Deny when the requested item is
// no longer in the queue (already approved/denied or never existed).
var ErrNotFound = errors.New("pending: item not found")

// memoryStore is the in-memory Store. Concurrency-safe via a mutex;
// id allocation uses a monotonic counter scoped per-process so tests
// (which construct fresh Stores) see deterministic ids starting at 1.
type memoryStore struct {
	mu     sync.Mutex
	items  map[string]Item
	order  []string // append order; iterated by List for stable reads
	nextID uint64
	now    func() time.Time
}

// NewMemoryStore constructs a fresh in-memory queue. The now closure
// is injected so tests can pin CreatedAt timestamps.
func NewMemoryStore(now func() time.Time) Store {
	if now == nil {
		now = time.Now
	}
	return &memoryStore{
		items: make(map[string]Item),
		now:   now,
	}
}

func (s *memoryStore) Append(_ context.Context, item Item) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := formatID(s.nextID)
	item.ID = id
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now().UTC()
	}
	s.items[id] = item
	s.order = append(s.order, id)
	return id, nil
}

func (s *memoryStore) List(_ context.Context) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, 0, len(s.order))
	for _, id := range s.order {
		if it, ok := s.items[id]; ok {
			out = append(out, it)
		}
	}
	// Defensive sort by CreatedAt — items appended out of monotonic
	// order (e.g. tests pinning timestamps) still surface oldest-first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memoryStore) Approve(_ context.Context, id string) (Item, error) {
	return s.remove(id)
}

func (s *memoryStore) Deny(_ context.Context, id string) (Item, error) {
	return s.remove(id)
}

func (s *memoryStore) remove(id string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	delete(s.items, id)
	for i, x := range s.order {
		if x == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return it, nil
}

// formatID renders a counter as a fixed-width zero-padded decimal so
// lexicographic sort matches numeric sort. The exact format is an
// implementation detail; consumers treat IDs as opaque.
func formatID(n uint64) string {
	const width = 12
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte('0' + n%10)
		n /= 10
	}
	return string(out)
}
