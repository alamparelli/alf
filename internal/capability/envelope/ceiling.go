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
//	memory:  agent-mediated   (no MemoryHandle exists by design)
//	events:  own topics only   (exports OK; subscribes from another
//	                            cap = cross-flow widening, refused)
//	http:    none              (deferred block — already rejected)
//	exec:    none              (deferred block — already rejected)
//	secrets: none              (deferred block — already rejected)
//	fs:      own-dir           (schema rejects absolute / "..")
//	tools:   own declares OK   (forge gates the runtime authority)
//
// Pre-condition: manifest already passed Validate() — the deferred-
// block sentinels (http/exec/secrets/memory) have already been
// rejected upstream. This function adds the surfaces that pass
// schema validation but exceed the Tier-2 ceiling.
//
// Today the only check that's not implicit in Validate is the
// `events.subscribes` rejection: a manifest that subscribes to
// another cap's events is requesting cross-cap authority, which
// the local daemon key cannot grant per §7.3. The user-endorsed
// key (Tier 3) is the right signer for that flow.
//
// Returns ErrCeilingExceeded with the specific surface that
// triggered. The signer maps this to a user-facing message
// pointing at `alf keygen`.
//
// Future widenings (when http/exec/secrets/memory blocks land)
// extend this function with their corresponding ceiling rules.
// Existing callers do not need to change — adding a check here
// tightens the gate for every Tier-2 signer.
func EnforceTier2Ceiling(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrCeilingExceeded)
	}
	if len(m.Events.Subscribes) > 0 {
		return fmt.Errorf("%w: events.subscribes (cross-flow from %d publisher(s)) requires user-endorsed key — re-sign with `alf sign --key user-endorsed` after `alf keygen`",
			ErrCeilingExceeded, len(m.Events.Subscribes))
	}
	return nil
}
