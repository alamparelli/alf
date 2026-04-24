package handle

import (
	"context"
	"sync/atomic"

	"github.com/alamparelli/alf/internal/capability"
)

// ToolInvoker is the narrow interface ToolHandle calls into to actually
// dispatch a tool invocation. Production wiring plugs in Runtime's tool
// dispatcher at forge time (step 8); tests pass a stub. The handle
// enforces scope and revocation; the invoker just runs the named tool.
//
// The invoker does NOT receive the owner identity — the handle's owner
// is baked in at forge time, and the invoker is constructed by Runtime
// with knowledge of who will call through it. Keeping Invoker narrow
// prevents capabilities from spoofing identity by passing a different
// owner string.
type ToolInvoker interface {
	Invoke(ctx context.Context, toolID capability.ID, input capability.Input) (capability.Output, error)
}

// ToolScope lists the tool IDs a capability may invoke. Exact match only
// — no wildcards. Declaring "any tool" is not supported by design: the
// manifest names each tool the capability depends on so the install-time
// UI can surface the coupling (§3.3 events style) and revocation can
// cascade when a depended-on tool is uninstalled (§8).
type ToolScope struct {
	Allowed []capability.ID
}

// Allows reports whether toolID is in scope.
func (s ToolScope) Allows(toolID capability.ID) bool {
	if toolID == "" {
		return false
	}
	for _, allowed := range s.Allowed {
		if allowed == toolID {
			return true
		}
	}
	return false
}

// ToolHandle grants scoped invocation of other capabilities (tools).
// Non-serializable. Revocation: Instance.Close flips revoked; in-flight
// invocations get their context cancelled via lifecycleCtx.
type ToolHandle struct {
	_ [0]noSerialize

	owner        capability.ID
	scope        ToolScope
	invoker      ToolInvoker
	lifecycleCtx context.Context
	revoked      atomic.Bool
}

// NewToolHandle constructs a tool-invocation handle scoped to the given
// tool IDs. The invoker is held by unexported field so the capability
// cannot reach around the scope check.
func NewToolHandle(owner capability.ID, scope ToolScope, invoker ToolInvoker) *ToolHandle {
	return &ToolHandle{owner: owner, scope: scope, invoker: invoker}
}

// Invoke dispatches the named tool if scope allows it and the handle has
// not been revoked. The caller ctx is merged with lifecycleCtx so
// Instance.Close() aborts in-flight invocations.
func (h *ToolHandle) Invoke(ctx context.Context, toolID capability.ID, input capability.Input) (capability.Output, error) {
	if h.revoked.Load() {
		return capability.Output{}, ErrRevoked
	}
	if !h.scope.Allows(toolID) {
		return capability.Output{}, ErrOutOfScope
	}
	opCtx, cancel := mergeContexts(ctx, h.lifecycleCtx)
	defer cancel()
	if h.invoker == nil {
		return capability.Output{}, ErrOutOfScope
	}
	return h.invoker.Invoke(opCtx, toolID, input)
}

// Owner returns the capability ID this handle was forged for.
func (h *ToolHandle) Owner() capability.ID { return h.owner }

// MarshalJSON implements §4.2 invariant 1.
func (h *ToolHandle) MarshalJSON() ([]byte, error) {
	return nil, ErrHandleNonSerializable
}

// attachLifecycle binds the handle to the Instance lifecycle context.
func (h *ToolHandle) attachLifecycle(ctx context.Context) { h.lifecycleCtx = ctx }
