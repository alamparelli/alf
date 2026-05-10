package wasm

import (
	"errors"
	"fmt"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// ErrKindForbiddenByLoader is returned by preLoad when a manifest declares
// a kind that the on-disk WASM loader is not authorised to instantiate.
//
// Doctrine: ARCHITECTURE-SECURITY.md §4.1 — Go-kind (bash-tool, python-tool,
// go-tool, go-app, marketplace-app) is reserved for in-binary maintainer
// code registered via capability_adapter.go. The WASM loader scans
// ~/data/tools/<id>/ and ~/data/apps/<slug>/; the only kinds it may
// instantiate from those paths are the WASM-isolation kinds plus skill
// (prompt-only, no executable) and provider variants (#392).
//
// This is the structural complement to MANIFEST-SCHEMA.md §5 (permission
// ceiling per signer tier). Kind admission is per-loader; permission
// ceiling stays per-tier; they are orthogonal.
var ErrKindForbiddenByLoader = errors.New("kind forbidden by on-disk loader (§4.1)")

// loaderAdmittedKinds is the allowlist for kinds the WASM loader may
// instantiate from disk. KindMarketplaceApp is intentionally absent —
// it is retired per MANIFEST-SCHEMA.md §3.3 and reachable only as
// dead code in legacy verify shims kept for compatibility.
var loaderAdmittedKinds = map[envelope.ManifestKind]struct{}{
	envelope.KindWASMTool:           {},
	envelope.KindWASMApp:            {},
	envelope.KindSkill:              {},
	envelope.KindLLMProvider:        {},
	envelope.KindCapabilityProvider: {},
}

// IsLoaderAdmittedKind reports whether a manifest kind may be instantiated
// by the on-disk WASM loader. Exported for archtest invariants and admin
// tooling that needs to mirror the lockdown decision before invoking the
// loader.
func IsLoaderAdmittedKind(k envelope.ManifestKind) bool {
	_, ok := loaderAdmittedKinds[k]
	return ok
}

// checkKindAdmission returns nil iff the manifest's kind is in the
// loader allowlist, otherwise wraps ErrKindForbiddenByLoader with the
// offending value for the operator log line.
func checkKindAdmission(m *envelope.Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrKindForbiddenByLoader)
	}
	if !IsLoaderAdmittedKind(m.Kind) {
		return fmt.Errorf("%w: got %q", ErrKindForbiddenByLoader, m.Kind)
	}
	return nil
}
