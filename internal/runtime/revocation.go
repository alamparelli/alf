package runtime

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// ErrRevokedBetweenVerifyAndTrack is returned by trackLive when the
// trust store says the bundle's signer (or one of its provider
// dependencies) is revoked with a not-valid-after timestamp at-or-
// before the bundle's SignedAt. This is the SEC-080-001 race-close:
// envelope.Verify saw the key as trusted, but a SIGHUP-driven
// truststore.Load() between Verify and trackLive flipped it. The
// in-flight Instance never enters the live registry, never receives
// a forge handle the caller can use, and the caller is responsible
// for closing the Instance.
//
// Why surface this at trackLive rather than re-call Verify: Verify
// is pure (canonicalisation + signature) and was already correct at
// the moment it ran; the only thing that changed is the trust store
// view. Holding liveMu across the recheck guarantees that any
// concurrent RevokeByKey for this signer either ran BEFORE we took
// the lock (in which case our recheck observes the revocation and
// refuses) or runs AFTER we released it (in which case our entry is
// in i.live and RevokeByKey closes it the normal way).
var ErrRevokedBetweenVerifyAndTrack = errors.New("runtime: signer revoked between envelope.Verify and trackLive (SEC-080-001 race close)")

// liveEntry binds a forged *handle.Instance back to the verification
// fact that produced it. The Instantiator keeps one entry per
// InstantiateVerified result; entries self-prune when the Instance's
// lifecycle context fires (either via Instance.Close or via
// RevokeByKey below).
//
// SignerID + SignedAt come from the bundle's signer (used by
// RevokeByKey for direct revocation). DependsOn is the set of provider
// fingerprints this Instance's manifest depended on (#392 Stage 5
// cascade): when ANY of those keys is revoked, this Instance must
// also close even if its own signer is not the revoked key.
type liveEntry struct {
	instance  *handle.Instance
	owner     capability.ID
	signerID  envelope.KeyID
	signedAt  time.Time
	dependsOn []envelope.KeyID
}

// trackLive registers an Instance with the live registry and spawns a
// watcher goroutine that drops the entry when the Instance's lifecycle
// context cancels. Called by InstantiateVerified after ForgeInstance
// succeeds.
//
// dependsOn is the set of provider keys this Instance is affected by
// (computed from the verified manifest's [[depends]] entries, with
// the alf: namespace excluded — alf core kinds are not provider-keyed).
// Revoking any of those keys closes this Instance via the cascade path
// in RevokeByKey.
//
// SEC-080-001: revoker is the same trust store envelope.Verify
// consulted; trackLive re-checks the signer + every dependsOn key
// against it under liveMu. If the signer was revoked with a
// not-valid-after at-or-before signedAt — i.e. a SIGHUP-driven
// Load() between Verify and now flipped the key — the Instance is
// NOT registered and ErrRevokedBetweenVerifyAndTrack is returned.
// The caller (InstantiateVerified) closes the Instance and
// propagates the error so the bundle never reaches a live forge
// handle. The lock ordering closes the race: any concurrent
// RevokeByKey for this signer either runs BEFORE we take liveMu (in
// which case our recheck sees the revocation and refuses) or AFTER
// we release it (in which case our entry is already in i.live and
// the cascade closes it normally).
//
// The watcher goroutine costs ~2 KB stack per live Instance — acceptable
// for the v0.8.0 single-host scale (dozens of caps). If we ever need to
// scale to thousands, replace with a finalizer or a periodic sweep.
func (i *Instantiator) trackLive(inst *handle.Instance, revoker envelope.Revoker, signerID envelope.KeyID, signedAt time.Time, dependsOn []envelope.KeyID) error {
	if inst == nil {
		return nil
	}
	entry := liveEntry{
		instance:  inst,
		owner:     inst.Owner,
		signerID:  signerID,
		signedAt:  signedAt,
		dependsOn: dependsOn,
	}
	i.liveMu.Lock()
	if revoker != nil {
		if cutoff, ok := revoker.RevokedAfter(signerID); ok && !signedAt.Before(cutoff) {
			i.liveMu.Unlock()
			return fmt.Errorf("%w: signer=%s signed_at=%s revoked_after=%s",
				ErrRevokedBetweenVerifyAndTrack,
				signerID.Hex(),
				signedAt.Format(time.RFC3339),
				cutoff.Format(time.RFC3339))
		}
		for _, dep := range dependsOn {
			if cutoff, ok := revoker.RevokedAfter(dep); ok && !signedAt.Before(cutoff) {
				i.liveMu.Unlock()
				return fmt.Errorf("%w: depends-on provider=%s signed_at=%s revoked_after=%s",
					ErrRevokedBetweenVerifyAndTrack,
					dep.Hex(),
					signedAt.Format(time.RFC3339),
					cutoff.Format(time.RFC3339))
			}
		}
	}
	i.live = append(i.live, entry)
	i.liveMu.Unlock()

	go func() {
		<-inst.Context().Done()
		i.liveMu.Lock()
		out := i.live[:0]
		for _, e := range i.live {
			if e.instance == inst {
				continue
			}
			out = append(out, e)
		}
		i.live = out
		i.liveMu.Unlock()
	}()
	return nil
}

// RevokeByKey closes every live Instance forged from a bundle signed
// by the given key fingerprint OR depending on a capability-provider
// signed by it (#392 Stage 5 cascade). Returns the slice of
// capability IDs that were closed — order is the order they were
// registered.
//
// Two close reasons surface in the audit log so the operator can tell
// "your bundle was directly signed by the revoked key" apart from
// "you depend on a provider that was revoked":
//   - "signed by revoked key" — direct revocation (existing path)
//   - "depends on revoked provider key" — cascade revocation (new)
//
// The CLI surface (`alf trust revoke <fp>`) reaches this method via
// the admin boundary; tests reach it via the package-private path.
//
// Concurrency: matched Instances are removed from the live slice
// under the mutex, then Close() is called outside the lock. Close()
// is idempotent (the per-Instance closeOnce guards), so a concurrent
// external Close()+RevokeByKey on the same Instance is safe.
//
// Audit: a structured log line is emitted per closed Instance via
// the configured revocationLogger (default log.Printf). The full
// audit stream design is part of #396 itself and lands later in the
// milestone.
func (i *Instantiator) RevokeByKey(id envelope.KeyID) []capability.ID {
	i.liveMu.Lock()
	type closeRecord struct {
		inst   *handle.Instance
		owner  capability.ID
		reason string
	}
	var matched []closeRecord
	out := i.live[:0]
	for _, e := range i.live {
		if e.signerID == id {
			matched = append(matched, closeRecord{
				inst:   e.instance,
				owner:  e.owner,
				reason: "signed by revoked key",
			})
			continue
		}
		// Cascade — does this Instance depend on the revoked key?
		cascade := false
		for _, dep := range e.dependsOn {
			if dep == id {
				cascade = true
				break
			}
		}
		if cascade {
			matched = append(matched, closeRecord{
				inst:   e.instance,
				owner:  e.owner,
				reason: "depends on revoked provider key",
			})
			continue
		}
		out = append(out, e)
	}
	i.live = out
	logger := i.revocationLogger
	i.liveMu.Unlock()

	if logger == nil {
		logger = log.Printf
	}
	closedIDs := make([]capability.ID, 0, len(matched))
	for _, r := range matched {
		logger("[revocation] closing instance %s — %s %s", r.owner, r.reason, id.Hex())
		r.inst.Close()
		closedIDs = append(closedIDs, r.owner)
	}
	return closedIDs
}

// LiveCount returns the number of currently-tracked Instances. Used by
// tests and by the future status surface (`alf status`); the production
// hot path never reads it.
func (i *Instantiator) LiveCount() int {
	i.liveMu.Lock()
	defer i.liveMu.Unlock()
	return len(i.live)
}

// WithRevocationLogger overrides the default log.Printf-based
// revocation audit sink. Tests pass a recorder so they can assert on
// the emitted lines; production daemons leave the default until the
// proper audit stream lands later in #396.
func WithRevocationLogger(fn func(format string, args ...any)) InstantiatorOption {
	return func(i *Instantiator) { i.revocationLogger = fn }
}

