package sandbox

import "context"

// policyCtxKey is a typed, unexported key for the single Policy installed on a
// ctx by Apply. A second Apply on the same ctx REPLACES the Policy; it never
// accumulates. This implements ARCHITECTURE-v0.7.10.md §2.4 hard rule:
// "one Policy applies to one Capability — no implicit accumulation".
type policyCtxKey struct{}

// New returns the default Sandbox implementation.
//
// Step 3 scaffold: Apply is a no-op that only installs the Policy on the ctx;
// facet enforcement (filesystem, network, secrets, integrity) lands in C3–C6.
// WASM can plug in its own Sandbox implementation later with <200 LoC.
func New() Sandbox {
	return defaultSandbox{}
}

type defaultSandbox struct{}

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
// Step 3 scaffold: this is a no-op enforcement layer — the Policy is stashed
// on the ctx but nothing consults it yet. C3 (network), C4 (secrets), C5
// (exec/filesystem), C6 (integrity) will each read the Policy from ctx and
// enforce their facet.
func (defaultSandbox) Apply(ctx context.Context, _ ManifestView, policy Policy) (context.Context, error) {
	return context.WithValue(ctx, policyCtxKey{}, policy), nil
}

// PolicyFrom returns the Policy installed on ctx by Apply, or (zero, false)
// if the ctx is not sandboxed. Facet enforcers (C3+) call this to read the
// active Policy at enforcement time.
func PolicyFrom(ctx context.Context) (Policy, bool) {
	p, ok := ctx.Value(policyCtxKey{}).(Policy)
	return p, ok
}
