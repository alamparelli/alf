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

// Policy is the effective, derived set of rules applied to one Capability.
type Policy struct {
	FileAccess FileRules
	Network    NetworkRules
	Secrets    SecretRules
	Tier       Tier
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
