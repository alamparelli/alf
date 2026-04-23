package runtime

import "github.com/alamparelli/alf/internal/capability"

// *capability.Registry satisfies CapabilityRegistry directly through its
// Resolve + List methods (added in #340 R4b). This compile-time assertion
// fails the build if the capability package drifts out of sync with the
// Runtime contract.
//
// Keeping the check in runtime (not capability) preserves the dependency
// edge runtime → capability: capability/ never imports runtime/.
var _ CapabilityRegistry = (*capability.Registry)(nil)
