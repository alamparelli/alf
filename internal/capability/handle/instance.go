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

// Instance aggregates every handle a capability received. A nil field means
// the manifest did not declare that resource and the capability has no way
// to reach it.
type Instance struct {
	Owner        capability.ID
	FS           *FSHandle
	HTTP         *HTTPHandle
	lifecycleCtx context.Context
	cancel       context.CancelFunc
	closeOnce    sync.Once
}

// NewInstance creates an Instance parented by ctx. Handles passed in are
// re-parented to the Instance lifecycle so Close() cancels every in-flight
// operation across all handles.
func NewInstance(ctx context.Context, owner capability.ID, fs *FSHandle, httpH *HTTPHandle) *Instance {
	lc, cancel := context.WithCancel(ctx)
	inst := &Instance{
		Owner:        owner,
		lifecycleCtx: lc,
		cancel:       cancel,
	}
	if fs != nil {
		fs.lifecycleCtx = lc
		inst.FS = fs
	}
	if httpH != nil {
		httpH.attachLifecycle(lc)
		inst.HTTP = httpH
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
	})
}
