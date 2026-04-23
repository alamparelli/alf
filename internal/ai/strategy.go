package ai

import "context"

// Strategy describes how to chain one or more Engine.Run calls to complete
// a single turn. Concrete strategies cover patterns like single-shot,
// retry-on-error, chain-of-thought, parallel fan-out, or tool-loop
// continuation.
//
// Strategy belongs to the ai contract, not to ai's implementations.
// Concrete orchestrators (the classifier from router/, the multi-agent
// coordinator from agents/) live in internal/runtime/ because they import
// memory, skills, tooling, or controlcenter — which ai/ must not depend
// on per technical/ARCHITECTURE-v0.7.10.md §4.
//
// A Strategy receives the Engine it should drive, rather than holding one,
// so Runtime can plug the same Strategy into different Engines (test
// doubles, provider-backed engines, future wazero-backed engines).
type Strategy interface {
	Run(ctx context.Context, engine Engine, req Request) (<-chan Event, error)
}
