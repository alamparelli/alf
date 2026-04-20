package integrity

// NewTestGuardWithQuarantine builds a minimal IntegrityGuard preseeded with
// a quarantine map, for use in cross-package tests that exercise consumers
// (e.g. tooling.Executor refusing to run a quarantined tool).
//
// Production code must not call this constructor — use NewIntegrityGuard.
func NewTestGuardWithQuarantine(quarantined map[string]QuarantinedTool) *IntegrityGuard {
	return &IntegrityGuard{quarantined: quarantined}
}
