package runtime

import (
	"log"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

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
// The watcher goroutine costs ~2 KB stack per live Instance — acceptable
// for the v0.8.0 single-host scale (dozens of caps). If we ever need to
// scale to thousands, replace with a finalizer or a periodic sweep.
func (i *Instantiator) trackLive(inst *handle.Instance, signerID envelope.KeyID, signedAt time.Time, dependsOn []envelope.KeyID) {
	if inst == nil {
		return
	}
	entry := liveEntry{
		instance:  inst,
		owner:     inst.Owner,
		signerID:  signerID,
		signedAt:  signedAt,
		dependsOn: dependsOn,
	}
	i.liveMu.Lock()
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

