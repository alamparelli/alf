package envelope

import (
	"errors"
	"fmt"
)

// ErrCeilingExceeded signals SEC-004: a manifest declares
// authorities beyond what the §7.3 Tier-2 (local-daemon) key may
// pre-approve. The signer rejects such manifests with this error;
// the operator must re-sign with the user-endorsed key (Tier 3) via
// `alf keygen` + `alf sign --key user-endorsed`.
var ErrCeilingExceeded = errors.New("envelope: manifest exceeds Tier-2 ceiling — user-endorsed key required")

// EnforceTier2Ceiling implements the §7.3 ceiling for the local
// daemon key per docs/ARCHITECTURE-SECURITY.md:
//
//	kind:         wasm-tool / wasm-app / skill / llm-provider / marketplace-app
//	              (capability-provider widens trust surface → Tier 3 only)
//	memory:       agent-mediated   (no MemoryHandle exists by design)
//	events:       own topics only   (exports OK; subscribes from another
//	                                 cap = cross-flow widening, refused)
//	http:         none              ([[http.scopes]] widens trust surface
//	                                 → Tier 3 only, #421)
//	exec:         none              (deferred block — already rejected)
//	secrets:      none              (deferred block — already rejected)
//	fs:           own-dir           (schema rejects absolute / "..")
//	tools:        own declares OK   (forge gates the runtime authority)
//	depends:      alf: namespace only
//	              (cross-publisher deps widen trust surface → Tier 3)
//	raw_imports:  none
//	              (WASI escape hatch widens ambient surface → Tier 3)
//
// Pre-condition: manifest already passed Validate() — the deferred-
// block sentinels (http/exec/secrets/memory) have already been
// rejected upstream. This function adds the surfaces that pass
// schema validation but exceed the Tier-2 ceiling.
//
// Returns ErrCeilingExceeded with the specific surface that
// triggered. The signer maps this to a user-facing message
// pointing at `alf keygen`.
//
// SEC-080-006: previously this function checked only
// `events.subscribes`. Three further surfaces pass schema
// validation but widen the trust envelope beyond what the local
// daemon key may pre-approve:
//
//   - Kind == capability-provider: registers new handle kinds in
//     the runtime registry under the daemon key's fingerprint.
//     The operator never reviewed those exports → Tier 3 only.
//   - [[depends]] with non-"alf" namespace: pulls authority from
//     another publisher. Today forgeGrants does not yet wire
//     non-alf deps into authority (Stage 5+); the moment it does,
//     a Tier-2 signature would silently widen via the dependency
//     chain. Fail-closed at the gate.
//   - [[raw_imports]]: even allowlisted WASI imports are not
//     ambient defaults — the daemon-key path is "LLM authors with
//     the ambient defaults"; raw imports need explicit operator
//     review.
//
// Future widenings (when http/exec/secrets/memory blocks land)
// extend this function with their corresponding ceiling rules.
// Existing callers do not need to change — adding a check here
// tightens the gate for every Tier-2 signer.
func EnforceTier2Ceiling(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrCeilingExceeded)
	}
	if m.Kind == KindCapabilityProvider {
		return fmt.Errorf("%w: kind = capability-provider widens the trust surface (registers new handle kinds in the runtime registry) — re-sign with `alf sign` after `alf keygen` (Tier 3)",
			ErrCeilingExceeded)
	}
	if len(m.Events.Subscribes) > 0 {
		return fmt.Errorf("%w: events.subscribes (cross-flow from %d publisher(s)) requires user-endorsed key — re-sign with `alf sign` after `alf keygen`",
			ErrCeilingExceeded, len(m.Events.Subscribes))
	}
	if len(m.HTTP.Scopes) > 0 {
		return fmt.Errorf("%w: [[http.scopes]] (%d scope(s)) widens the trust surface (outbound HTTP) — re-sign with `alf sign` after `alf keygen` (Tier 3)",
			ErrCeilingExceeded, len(m.HTTP.Scopes))
	}
	for _, dep := range m.Depends {
		ns, _ := dep.SplitHandle()
		if ns != reservedNamespaceALF {
			return fmt.Errorf("%w: [[depends]] handle %q (namespace %q != %q) pulls authority from another publisher — re-sign with `alf sign` after `alf keygen`",
				ErrCeilingExceeded, dep.Handle, ns, reservedNamespaceALF)
		}
	}
	if len(m.RawImports) > 0 {
		return fmt.Errorf("%w: [[raw_imports]] (%d entry/entries) widens the WASI surface — re-sign with `alf sign` after `alf keygen`",
			ErrCeilingExceeded, len(m.RawImports))
	}
	return nil
}
