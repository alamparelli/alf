package wasm

import (
	"sync"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// hostHandleRegistry is the per-runtime mapping from guest module name
// to the *handle.Instance the host functions should consult when that
// guest invokes a host import. Without it, the shared "alf" host module
// (one per runtime, per wazero's name-uniqueness rule) cannot route
// per-guest authority.
//
// The guest's wazero module name (set by Runtime.Instantiate via
// WithName(string(manifest.ID))) is the key. Host functions read it
// from `api.Module.Name()` on every call and look up the matching
// Instance, then reach into `inst.FS`, `inst.HTTP`, etc. for the
// specific handle. Lookup miss returns a nil Instance and the host
// function refuses to operate (errIO) — a deliberate refusal to
// proceed without a registered authority record (defense against
// guests instantiated outside the official forge path).
//
// History: this type was previously hostFSRegistry and stored only
// *handle.FSHandle. #421 Wave 2 widened it to *handle.Instance so
// alf_http_request (and future alf_exec_*, alf_secrets_*) can share
// the same dispatch table.
//
// Concurrency: Register / Unregister take the write lock; Lookup
// takes the read lock. Lookups happen on every host call, so the
// RWMutex split matters under load.
type hostHandleRegistry struct {
	mu        sync.RWMutex
	instances map[string]*handle.Instance
}

func newHostHandleRegistry() *hostHandleRegistry {
	return &hostHandleRegistry{instances: make(map[string]*handle.Instance)}
}

// Register binds an Instance to a guest module name. Replaces any
// previous binding for the same name (the prior guest must have been
// closed via Unregister, but we don't enforce — last-write-wins is
// fine because guest names are unique per Instantiate call).
func (r *hostHandleRegistry) Register(guestName string, inst *handle.Instance) {
	if guestName == "" || inst == nil {
		return
	}
	r.mu.Lock()
	r.instances[guestName] = inst
	r.mu.Unlock()
}

// Unregister removes the binding for guestName. Idempotent — safe to
// call on a name that was never registered (covers cleanup paths
// after partial failure).
func (r *hostHandleRegistry) Unregister(guestName string) {
	if guestName == "" {
		return
	}
	r.mu.Lock()
	delete(r.instances, guestName)
	r.mu.Unlock()
}

// Lookup returns the Instance for guestName or nil if none is
// registered. Host functions use the nil to refuse the call (errIO).
func (r *hostHandleRegistry) Lookup(guestName string) *handle.Instance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.instances[guestName]
}
