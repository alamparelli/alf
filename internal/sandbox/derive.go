package sandbox

import (
	"context"
	"fmt"
)

// identityCtxKey is a typed, unexported key for the single Identity stashed
// on a ctx by Apply. A second Apply on the same ctx REPLACES the Identity;
// it never accumulates — the one-Identity-per-ctx invariant mirrors the
// one-Policy-per-Capability rule from ARCHITECTURE-v0.7.10.md §2.4.
//
// Narrowed from the pre-0.8.0 policyCtxKey in #406 section 4: authority no
// longer rides on ctx (see Identity docstring in sandbox.go).
type identityCtxKey struct{}

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

// Apply verifies the capability's integrity and tags ctx with an Identity
// for audit/telemetry. The returned ctx MUST be the one passed to
// Capability.Execute; any other ctx is unsandboxed.
//
// Order of operations:
//  1. Integrity check (if wired via WithIntegrity). A failing check aborts
//     the call; Identity is never installed.
//  2. Identity stash on ctx (CapID + Tier).
//
// policy is accepted for signature stability — it remains the derivation
// output that Runtime.Instantiate (#391) consumes to forge capability
// handles with baked-in scope. Its authority-bearing fields (FileAccess,
// Network, Secrets) are NOT propagated via ctx. If a future facet needs to
// consult policy at enforcement time, it receives policy as an explicit
// parameter or a forged handle — never by reading ctx.
func (s defaultSandbox) Apply(ctx context.Context, m ManifestView, policy Policy) (context.Context, error) {
	if s.checker != nil {
		if err := s.checker.Verify(m.ID); err != nil {
			return nil, fmt.Errorf("sandbox: integrity check failed for %q: %w", m.ID, err)
		}
	}
	return context.WithValue(ctx, identityCtxKey{}, Identity{CapID: m.ID, Tier: policy.Tier}), nil
}

// IdentityFrom returns the Identity stashed on ctx by Apply, or
// (zero, false) if ctx is not sandboxed. Safe to call anywhere — reading
// Identity cannot grant access, only log who is acting.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(Identity)
	return id, ok
}
