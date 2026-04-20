package sandbox

import (
	"context"
	"fmt"
)

// policyCtxKey is a typed, unexported key for the single Policy installed on a
// ctx by Apply. A second Apply on the same ctx REPLACES the Policy; it never
// accumulates. This implements ARCHITECTURE-v0.7.10.md §2.4 hard rule:
// "one Policy applies to one Capability — no implicit accumulation".
type policyCtxKey struct{}

// IntegrityChecker is the plug-point that lets Apply reject Capabilities
// whose backing binary / bundle has been tampered with. The sandbox root
// depends only on this tiny interface so sandbox/integrity can live as a
// sub-package and WASM can plug in a cosign-based implementation later.
//
// Verify receives the Capability ID (from ManifestView.ID) and returns
// a non-nil error if the capability is quarantined or fails integrity
// verification.
type IntegrityChecker interface {
	Verify(capID string) error
}

// Option configures the default Sandbox at construction time.
type Option func(*defaultSandbox)

// WithIntegrity wires an IntegrityChecker that Apply runs BEFORE installing
// the Policy. A failing check aborts Apply — the capability never gets a
// sandboxed ctx. Passing nil is a no-op.
//
// ARCHITECTURE-v0.7.10.md §2.4: "the integrity scan runs within Sandbox,
// not alongside."
func WithIntegrity(c IntegrityChecker) Option {
	return func(s *defaultSandbox) { s.checker = c }
}

// New returns the default Sandbox implementation.
//
// Step 3 scaffold: facet enforcement (filesystem, network, secrets) is not
// yet wired — the Policy is only installed on ctx for downstream enforcers
// to consult. Integrity is the only facet with a behavioural wire-in so far
// (via WithIntegrity), because it runs before Policy install.
// WASM can plug in its own Sandbox implementation later with <200 LoC.
func New(opts ...Option) Sandbox {
	s := defaultSandbox{}
	for _, opt := range opts {
		opt(&s)
	}
	return s
}

type defaultSandbox struct {
	checker IntegrityChecker
}

// Derive builds the effective Policy for a Capability from its Manifest view
// and the user tier. Current policy: straight projection from the declared
// permission set. Tier carries the aggregated envelope; per-tier widening or
// narrowing is layered in by the facets in C3–C5 as each one lands.
//
// Hard rule: derivation is deterministic. The same (m, tier) MUST produce
// the same Policy every time — no mutation, no ambient state.
func (defaultSandbox) Derive(m ManifestView, tier Tier) (Policy, error) {
	return Policy{
		FileAccess: FileRules{
			ReadPaths:  append([]string(nil), m.Permissions.FilePaths...),
			WritePaths: append([]string(nil), m.Permissions.FilePaths...),
		},
		Network: NetworkRules{
			AllowedDomains: append([]string(nil), m.Permissions.Networks...),
		},
		Secrets: SecretRules{
			KeyPatterns: append([]string(nil), m.Permissions.Secrets...),
		},
		Tier: tier,
	}, nil
}

// Apply installs policy on ctx so that the Capability identified by m runs
// under the enforced boundaries. The returned ctx MUST be the one passed to
// Capability.Execute; any other ctx is unsandboxed.
//
// Order of operations:
//  1. Integrity check (if wired via WithIntegrity). A failing check aborts
//     the call; the Policy is never installed.
//  2. Policy install on ctx.
//
// The network / secrets / exec facets are consulted at enforcement time by
// code that reads the Policy via PolicyFrom. Wiring those facets into Apply
// itself is Runtime's (#340) job.
func (s defaultSandbox) Apply(ctx context.Context, m ManifestView, policy Policy) (context.Context, error) {
	if s.checker != nil {
		if err := s.checker.Verify(m.ID); err != nil {
			return nil, fmt.Errorf("sandbox: integrity check failed for %q: %w", m.ID, err)
		}
	}
	return context.WithValue(ctx, policyCtxKey{}, policy), nil
}

// PolicyFrom returns the Policy installed on ctx by Apply, or (zero, false)
// if the ctx is not sandboxed. Facet enforcers (C3+) call this to read the
// active Policy at enforcement time.
func PolicyFrom(ctx context.Context) (Policy, bool) {
	p, ok := ctx.Value(policyCtxKey{}).(Policy)
	return p, ok
}
