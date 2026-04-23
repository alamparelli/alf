package provider

import (
	"context"
	"errors"

	"github.com/alamparelli/alf/internal/ai"
)

// NewRegistryEngine returns an ai.Engine that picks the concrete Provider
// per-call via Registry.ForBackend(req.Backend). An empty Backend uses the
// Registry's CLI default. Every other responsibility — splitPrompt, Usage
// surfacing, SystemPrompts merging — is delegated to the same per-Provider
// adapter used by NewEngine.
//
// #340 R5d introduced this so consumers that carry tier-level backend
// selection (scheduler, future chat_service) can route through Runtime
// without needing one Runtime per backend. The ai layer stays
// provider-agnostic: Backend is just a routing hint on ai.Request.
func NewRegistryEngine(r *Registry) ai.Engine {
	return &registryEngine{reg: r}
}

type registryEngine struct {
	reg *Registry
}

// Run dispatches to the Provider bound to req.Backend. A nil Registry is a
// configuration bug and surfaces immediately; a registered but missing
// Backend is also treated as a hard error rather than silently falling back
// to CLI — silent fallback would mask misconfigured tiers.
func (e *registryEngine) Run(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	if e.reg == nil {
		return nil, errors.New("provider.RegistryEngine: nil Registry")
	}
	prov := e.reg.ForBackend(req.Backend)
	if prov == nil {
		return nil, errors.New("provider.RegistryEngine: no Provider for backend " + req.Backend)
	}
	return NewEngine(prov).Run(ctx, req)
}

// Compile-time assertion: the adapter satisfies ai.Engine.
var _ ai.Engine = (*registryEngine)(nil)
