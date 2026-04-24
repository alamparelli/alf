// Package sandbox defines the target contract for the component that enforces
// access policies: firewall (network) + vault (secrets) + filesystem + integrity
// merged into one.
//
// This package is a Step 0 scaffold for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md). Signatures only — no implementation.
// Business code from firewall/, vault/, tooling/sandbox*.go, tooling/integrity.go,
// and marketplace/permissions.go migrates here in Step 3.
//
// Dependency rule: sandbox MUST NOT import capability, memory, ai, or runtime.
// It receives the capability Manifest as a value — never imports the package.
//
// Hard rules:
//   - One Policy applies to one Capability. No implicit accumulation.
//   - Policy is derived from the capability Manifest + user tier. Never ad-hoc.
//   - Firewall + Vault + Filesystem + Integrity are four facets of the same Sandbox.
package sandbox

import "context"

// Tier represents the user's aggregated capability envelope (free, pro, ...).
type Tier string

// FileRules authorise filesystem access within a sandboxed ctx.
type FileRules struct {
	ReadPaths  []string // glob patterns
	WritePaths []string
}

// NetworkRules authorise outbound network access.
type NetworkRules struct {
	AllowedDomains []string
	AllowedCIDRs   []string
}

// SecretRules authorise vault key lookups.
type SecretRules struct {
	KeyPatterns []string
}

// Policy is the effective, derived set of rules for one Capability.
//
// Policy is a *derivation output* — what Sandbox.Derive produces from a
// manifest + tier. Under the ocap model (docs/ARCHITECTURE-SECURITY.md §3.1),
// Policy is consumed by Runtime.Instantiate at forge time to bake scope into
// capability handles. It is NOT propagated via ctx — see Identity below and
// the commentary on Sandbox.Apply. A Policy value that escapes its
// forge-time role should be treated as dead data.
type Policy struct {
	FileAccess FileRules
	Network    NetworkRules
	Secrets    SecretRules
	Tier       Tier
}

// Identity carries per-call identity + audit metadata on the sandboxed ctx.
// It contains NO authority surface — no allow/deny fields, no permissions,
// no policy. Authority, under ocap (§3.1), lives in the handles a capability
// holds, forged by Runtime.Instantiate from the verified manifest. Anything
// on ctx can be manipulated by code that holds the ctx; ctx is the wrong
// home for authority.
//
// Narrowed from the pre-0.8.0 Policy-on-ctx surface in #406 section 4.
type Identity struct {
	// CapID mirrors ManifestView.ID — the caller capability for this call.
	CapID string
	// Tier is the aggregated user envelope; surfaced for audit logs and
	// downstream telemetry, never as a policy gate.
	Tier Tier
}

// ManifestView is the minimal shape Sandbox needs to derive a Policy.
// The runtime adapts capability.Manifest → ManifestView so sandbox never
// imports the capability package.
type ManifestView struct {
	ID          string
	Permissions PermissionsView
}

// PermissionsView mirrors capability.PermissionSet without importing it.
type PermissionsView struct {
	FilePaths []string
	Networks  []string
	Secrets   []string
}

// Sandbox installs a Policy on a ctx so that the enclosed Capability.Execute
// runs under the enforced boundaries.
type Sandbox interface {
	// Apply returns a sandboxed ctx for the Capability identified by the view.
	// The returned ctx must be the one passed to Capability.Execute.
	Apply(ctx context.Context, m ManifestView, policy Policy) (context.Context, error)

	// Derive builds the effective Policy from a Manifest view and user tier.
	Derive(m ManifestView, tier Tier) (Policy, error)
}
