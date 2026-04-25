package events

import (
	"sync"

	"github.com/alamparelli/alf/internal/capability"
)

// CrossFlowRegistry is the lookup table the loader populates in pass 1
// (from events.exports of every signed manifest) and the instantiator
// queries in pass 2 to decide whether to forge an EventSub handle.
//
// Per §3.3, a subscriber's handle is materialised only when the cited
// publisher is installed AND its signed manifest declares the matching
// export. The registry is the single source of truth for that check —
// the instantiator never re-parses manifests, only asks the registry.
type CrossFlowRegistry interface {
	// RegisterExport records that publisher exports topic. Called once
	// per (publisher, topic) pair in pass 1. Idempotent: re-registering
	// the same pair is a no-op.
	RegisterExport(publisher capability.ID, topic string)

	// HasExport reports whether publisher has registered topic. Called
	// in pass 2 forge for every events.subscribes declaration.
	HasExport(publisher capability.ID, topic string) bool
}

// MemoryRegistry is the default in-memory implementation. Construct
// via NewMemoryRegistry. Safe for concurrent use; reads dominate so
// it uses an RWMutex.
type MemoryRegistry struct {
	mu      sync.RWMutex
	exports map[exportKey]struct{}
}

type exportKey struct {
	publisher capability.ID
	topic     string
}

// NewMemoryRegistry constructs an empty registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{exports: make(map[exportKey]struct{})}
}

// RegisterExport implements CrossFlowRegistry.
func (r *MemoryRegistry) RegisterExport(publisher capability.ID, topic string) {
	if topic == "" {
		return
	}
	r.mu.Lock()
	r.exports[exportKey{publisher: publisher, topic: topic}] = struct{}{}
	r.mu.Unlock()
}

// HasExport implements CrossFlowRegistry.
func (r *MemoryRegistry) HasExport(publisher capability.ID, topic string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.exports[exportKey{publisher: publisher, topic: topic}]
	return ok
}
