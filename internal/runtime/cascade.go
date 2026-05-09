package runtime

import (
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

// RevocationCascader is the discovery-side seam of #396 deliverable 2.
//
// The runtime cascade engine (Instantiator.RevokeByKey, shipped in
// #392 Stage 5) closes every live Instance directly signed by a
// revoked key OR depending on a revoked capability provider. What
// it does NOT do is observe the trust store — the daemon must call
// RevokeByKey when something the trust store reports as revoked
// becomes newly so. Two channels feed that discovery:
//
//  1. Operator path — `alf trust revoke <fp>` writes a `<keyid>.revoked`
//     sidecar; SIGHUP triggers DirTrustStore.Load() then this Refresh.
//  2. CRL path — crl.Refresher fetches a signed CRL, applies it via
//     MemoryTrustStore.ApplyCRL, then fires its OnApply callback
//     pointing at this Refresh.
//
// The Cascader reduces both to a single mechanism: snapshot the
// trust store's combined revoked map (operator-set strictest-wins
// CRL-set, the same merge that envelope.Verify uses), diff against
// the previous snapshot, and call RevokeByKey for every key whose
// state crossed the "newly-revoked or boundary-tightened" line.
//
// The first snapshot is taken at construction. Keys revoked at boot
// do NOT cascade: their bundles never made it past envelope.Verify
// in the first place, so there are no live Instances to close. Only
// transitions observed AFTER construction trigger work.
type RevocationCascader struct {
	inst        *Instantiator
	snapshotter func() map[envelope.KeyID]time.Time
	logf        func(format string, args ...any)

	mu   sync.Mutex
	last map[envelope.KeyID]time.Time
}

// NewRevocationCascader wires the cascader to a forge and a snapshot
// source. snapshotter is typically (*envelope.MemoryTrustStore).AllRevoked
// bound to the daemon's DirTrustStore (which embeds MemoryTrustStore).
//
// logf is used for one audit line per cascade transition (newly-revoked
// or tightened); nil falls back to a no-op so tests can stay quiet.
//
// The initial snapshot is taken eagerly so the first Refresh() has a
// baseline to diff against. Callers that want to cascade for keys
// revoked before construction must snapshot, mutate the store, then
// construct — but that flow is outside #396 D2 and would only matter
// if a key was added-then-revoked between Load() and NewRevocationCascader.
func NewRevocationCascader(
	inst *Instantiator,
	snapshotter func() map[envelope.KeyID]time.Time,
	logf func(format string, args ...any),
) *RevocationCascader {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	c := &RevocationCascader{
		inst:        inst,
		snapshotter: snapshotter,
		logf:        logf,
	}
	c.last = c.snapshotter()
	return c
}

// Refresh snapshots the current revoked set, diffs it against the
// previous snapshot, and calls Instantiator.RevokeByKey for each
// key whose state crossed one of two transitions:
//
//   - Newly revoked: key was not in the previous snapshot but is
//     present now (operator just ran `alf trust revoke`, or a CRL
//     refresh added an entry).
//   - Tightened: key was already revoked but the not-valid-after
//     timestamp moved STRICTLY EARLIER (operator overrode with
//     `alf trust revoke <fp> --at <earlier>` to record that
//     compromise actually started before the original boundary).
//     Bundles with signed-at in the new window now need to close.
//
// Keys whose timestamps moved later (operator softened, which the
// strictest-wins rule actually doesn't permit on the same channel —
// but a CRL entry getting later relative to an unchanged operator
// entry would be a no-op for our purposes) are skipped: nothing
// new to revoke. Keys that disappeared (operator ran
// `alf trust add` to re-trust, or a new CRL drops an entry) are
// also skipped — the strictest-wins channel that survived already
// reflects the right state, and any Instances that were closed
// stay closed.
//
// Returns the flat slice of capability IDs that were closed across
// all cascaded keys. The caller can log the count for operator-
// facing surfaces (status endpoint, SIGHUP audit line).
//
// Concurrency: a serialised lock protects the diff so two
// concurrent Refresh calls (one from SIGHUP, one from CRL OnApply)
// cannot race on the snapshot comparison and miss a transition.
func (c *RevocationCascader) Refresh() []capability.ID {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.snapshotter()
	var closed []capability.ID
	for k, t := range current {
		prev, ok := c.last[k]
		switch {
		case !ok:
			c.logf("[cascade] key newly revoked: %s not-valid-after=%s",
				k.Hex(), t.Format(time.RFC3339))
			closed = append(closed, c.inst.RevokeByKey(k)...)
		case t.Before(prev):
			c.logf("[cascade] key revocation tightened: %s not-valid-after=%s (was %s)",
				k.Hex(), t.Format(time.RFC3339), prev.Format(time.RFC3339))
			closed = append(closed, c.inst.RevokeByKey(k)...)
		}
	}
	c.last = current
	return closed
}
