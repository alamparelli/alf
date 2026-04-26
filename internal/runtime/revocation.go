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
// SignerID + SignedAt are kept here even though commit 2 of #396 only
// uses SignerID for RevokeByKey — commit 3 (trust-store not-valid-after
// enforcement) needs SignedAt and we'd rather not churn this struct
// across two commits.
type liveEntry struct {
	instance *handle.Instance
	owner    capability.ID
	signerID envelope.KeyID
	signedAt time.Time
}

// trackLive registers an Instance with the live registry and spawns a
// watcher goroutine that drops the entry when the Instance's lifecycle
// context cancels. Called by InstantiateVerified after ForgeInstance
// succeeds.
//
// The watcher goroutine costs ~2 KB stack per live Instance — acceptable
// for the v0.8.0 single-host scale (dozens of caps). If we ever need to
// scale to thousands, replace with a finalizer or a periodic sweep.
func (i *Instantiator) trackLive(inst *handle.Instance, signerID envelope.KeyID, signedAt time.Time) {
	if inst == nil {
		return
	}
	entry := liveEntry{
		instance: inst,
		owner:    inst.Owner,
		signerID: signerID,
		signedAt: signedAt,
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
// by the given key fingerprint. Returns the slice of capability IDs
// that were closed — order is the order they were registered.
//
// This is the user-facing primitive of #396 deliverable 3 (key-based
// revocation). The CLI surface (`alf trust revoke <fp>`) wires through
// this method via the admin boundary in a Stage 2 follow-up.
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
	var matched []*handle.Instance
	var closedIDs []capability.ID
	out := i.live[:0]
	for _, e := range i.live {
		if e.signerID == id {
			matched = append(matched, e.instance)
			closedIDs = append(closedIDs, e.owner)
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
	for idx, inst := range matched {
		logger("[revocation] closing instance %s — bundle signed by revoked key %s", closedIDs[idx], id.Hex())
		inst.Close()
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

