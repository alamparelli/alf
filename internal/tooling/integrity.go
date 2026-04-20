package tooling

import (
	"github.com/alamparelli/alf/internal/sandbox/integrity"
)

// This file is a thin re-export shim. The Integrity facet of Sandbox now
// lives at internal/sandbox/integrity (moved during #339 Step 3).
// tooling.Registry, tooling.Executor, and cmd/alf-daemon keep compiling
// unchanged; Runtime (#340) will migrate them off internal/tooling.

// IntegrityGuard is an alias for integrity.IntegrityGuard.
type IntegrityGuard = integrity.IntegrityGuard

// ManifestEntry is an alias for integrity.ManifestEntry.
type ManifestEntry = integrity.ManifestEntry

// QuarantinedTool is an alias for integrity.QuarantinedTool.
type QuarantinedTool = integrity.QuarantinedTool

// ErrToolQuarantined re-exports integrity.ErrToolQuarantined.
var ErrToolQuarantined = integrity.ErrToolQuarantined

// NewIntegrityGuard re-exports integrity.NewIntegrityGuard.
func NewIntegrityGuard(dataDir string, notify func(tool, oldHash, newHash string)) (*IntegrityGuard, error) {
	return integrity.NewIntegrityGuard(dataDir, notify)
}

// IsUserTool re-exports integrity.IsUserTool.
func IsUserTool(toolPath, dataDir string) bool {
	return integrity.IsUserTool(toolPath, dataDir)
}
