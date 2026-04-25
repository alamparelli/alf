package wasm

import (
	"sync"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// hostFSRegistry is the per-runtime mapping from guest module name to
// the FSHandle the host functions should use when that guest invokes
// alf_fs_*. Without it, the shared "alf" host module (one per runtime,
// per wazero's name-uniqueness rule) cannot route per-guest authority.
//
// The guest's wazero module name (set by Runtime.Instantiate via
// WithName(string(manifest.ID))) is the key. Host functions read it
// from `api.Module.Name()` on every call and look up the matching
// handle. Lookup miss returns errIO — a deliberate refusal to operate
// without a registered handle (defense against guests instantiated
// outside the official forge path).
//
// Concurrency: Register / Unregister take the write lock; Lookup
// takes the read lock. Lookups happen on every host call, so the
// RWMutex split matters under load.
type hostFSRegistry struct {
	mu      sync.RWMutex
	handles map[string]*handle.FSHandle
}

func newHostFSRegistry() *hostFSRegistry {
	return &hostFSRegistry{handles: make(map[string]*handle.FSHandle)}
}

// Register binds an FSHandle to a guest module name. Replaces any
// previous binding for the same name (the prior guest must have been
// closed via Unregister, but we don't enforce — last-write-wins is
// fine because guest names are unique per Instantiate call).
func (r *hostFSRegistry) Register(guestName string, fs *handle.FSHandle) {
	if guestName == "" || fs == nil {
		return
	}
	r.mu.Lock()
	r.handles[guestName] = fs
	r.mu.Unlock()
}

// Unregister removes the binding for guestName. Idempotent — safe to
// call on a name that was never registered (covers cleanup paths
// after partial failure).
func (r *hostFSRegistry) Unregister(guestName string) {
	if guestName == "" {
		return
	}
	r.mu.Lock()
	delete(r.handles, guestName)
	r.mu.Unlock()
}

// Lookup returns the FSHandle for guestName or nil if none is
// registered. Host functions use the nil to refuse the call (errIO).
func (r *hostFSRegistry) Lookup(guestName string) *handle.FSHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handles[guestName]
}
