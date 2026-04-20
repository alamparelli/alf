package capability

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is the unified catalog of every Capability ALF can execute —
// native tools, skills, and installed apps.
//
// This registry is the target surface the Runtime (Step 4) will consume.
// During Step 2 it lives alongside the legacy registries (tooling.Registry,
// skills.Catalog, marketplace.Manager) via dual-registration; those legacy
// registries are removed once all consumers have migrated.
type Registry struct {
	mu   sync.RWMutex
	caps map[ID]Capability
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{caps: make(map[ID]Capability)}
}

// Register adds a Capability to the registry. Returns an error if the
// Manifest ID is empty or already registered.
func (r *Registry) Register(c Capability) error {
	if c == nil {
		return fmt.Errorf("capability: cannot register nil")
	}
	m := c.Manifest()
	if m.ID == "" {
		return fmt.Errorf("capability: manifest ID is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.caps[m.ID]; exists {
		return fmt.Errorf("capability: ID %q already registered", m.ID)
	}
	r.caps[m.ID] = c
	return nil
}

// Replace upserts a Capability: if an entry with the same ID exists it is
// overwritten, otherwise the Capability is added. Use this when mirroring
// a mutable external source (e.g. skills reloaded from disk) where calling
// Register on every refresh would fail on duplicates.
func (r *Registry) Replace(c Capability) error {
	if c == nil {
		return fmt.Errorf("capability: cannot replace with nil")
	}
	m := c.Manifest()
	if m.ID == "" {
		return fmt.Errorf("capability: manifest ID is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps[m.ID] = c
	return nil
}

// Get returns the Capability registered under id, or (nil, false) if absent.
func (r *Registry) Get(id ID) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.caps[id]
	return c, ok
}

// Resolve is the Runtime-facing accessor: it satisfies the lookup half of
// runtime.CapabilityRegistry (defined in internal/runtime). Behaviour is
// identical to Get — Resolve exists to let *Registry match the Runtime
// interface without an adapter type.
func (r *Registry) Resolve(id ID) (Capability, bool) {
	return r.Get(id)
}

// List returns every registered Manifest, sorted by ID for determinism.
// It satisfies the enumeration half of runtime.CapabilityRegistry.
func (r *Registry) List() []Manifest {
	caps := r.All()
	out := make([]Manifest, 0, len(caps))
	for _, c := range caps {
		out = append(out, c.Manifest())
	}
	return out
}

// Len returns the number of registered capabilities.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.caps)
}

// All returns every registered Capability, sorted by ID for determinism.
func (r *Registry) All() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.caps))
	ids := make([]ID, 0, len(r.caps))
	for id := range r.caps {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		out = append(out, r.caps[id])
	}
	return out
}

// ByKind returns every registered Capability of the given Kind,
// sorted by ID for determinism.
func (r *Registry) ByKind(k Kind) []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]ID, 0, len(r.caps))
	for id, c := range r.caps {
		if c.Manifest().Kind == k {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Capability, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.caps[id])
	}
	return out
}
