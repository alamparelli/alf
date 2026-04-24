// Package handle carries the Tier 3.1 object-capability handle types used by
// the WASM runtime (and eventually all capability kinds). Runtime.Instantiate
// is the only site that forges an Instance; capabilities receive only what
// was explicitly granted via the manifest.
//
// See docs/ARCHITECTURE-SECURITY.md §3.1 + #391.
package handle

import (
	"context"
	"sync"

	"github.com/alamparelli/alf/internal/capability"
)

// Grants is the set of handles Runtime.Instantiate forged from a verified
// manifest. A nil field means the manifest did not declare that resource —
// the capability literally has no way to reach it. This struct mirrors the
// §3.1 per-resource handle table and grows as new Tier 3.1 handle types
// are added; existing call sites are insulated from new slots.
type Grants struct {
	FS      *FSHandle
	HTTP    *HTTPHandle
	Exec    *ExecHandle
	Secrets *SecretsHandle
	Tool    *ToolHandle
}

// Instance aggregates every handle a capability received. A nil field means
// the manifest did not declare that resource and the capability has no way
// to reach it.
type Instance struct {
	Owner        capability.ID
	FS           *FSHandle
	HTTP         *HTTPHandle
	Exec         *ExecHandle
	Secrets      *SecretsHandle
	Tool         *ToolHandle
	lifecycleCtx context.Context
	cancel       context.CancelFunc
	closeOnce    sync.Once
}

// NewInstance creates an Instance parented by ctx. Handles in g are
// re-parented to the Instance lifecycle so Close() cancels every in-flight
// operation across all handles. A nil Grants field means the manifest did
// not declare that resource.
func NewInstance(ctx context.Context, owner capability.ID, g Grants) *Instance {
	lc, cancel := context.WithCancel(ctx)
	inst := &Instance{
		Owner:        owner,
		lifecycleCtx: lc,
		cancel:       cancel,
	}
	if g.FS != nil {
		g.FS.lifecycleCtx = lc
		inst.FS = g.FS
	}
	if g.HTTP != nil {
		g.HTTP.attachLifecycle(lc)
		inst.HTTP = g.HTTP
	}
	if g.Exec != nil {
		g.Exec.attachLifecycle(lc)
		inst.Exec = g.Exec
	}
	if g.Secrets != nil {
		g.Secrets.attachLifecycle(lc)
		inst.Secrets = g.Secrets
	}
	if g.Tool != nil {
		g.Tool.attachLifecycle(lc)
		inst.Tool = g.Tool
	}
	return inst
}

// Context returns the instance lifecycle context. Cancelled by Close().
func (i *Instance) Context() context.Context {
	return i.lifecycleCtx
}

// Close cancels the instance lifecycle context, which propagates to every
// in-flight handle operation. Subsequent handle calls return ErrRevoked.
func (i *Instance) Close() {
	i.closeOnce.Do(func() {
		i.cancel()
		if i.FS != nil {
			i.FS.revoked.Store(true)
		}
		if i.HTTP != nil {
			i.HTTP.revoked.Store(true)
		}
		if i.Exec != nil {
			i.Exec.revoked.Store(true)
		}
		if i.Secrets != nil {
			i.Secrets.revoked.Store(true)
		}
		if i.Tool != nil {
			i.Tool.revoked.Store(true)
		}
	})
}
