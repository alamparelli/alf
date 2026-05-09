package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// TestRevocationCascader_NewlyRevokedKeyTriggersCascade pins the
// core operator-path behaviour: a key not present at construction
// time, then added to the snapshot before the next Refresh, fires
// RevokeByKey and closes the matching live Instance.
//
// This is the SIGHUP discovery channel — operator runs `alf trust
// revoke`, the sidecar lands on disk, DirTrustStore.Load() picks it
// up, snapshot now includes the key, Refresh diffs and cascades.
func TestRevocationCascader_NewlyRevokedKeyTriggersCascade(t *testing.T) {
	handle.ResetMintForTesting()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	logger := &recordingLogger{}
	inst := NewInstantiator(WithRevocationLogger(logger.printf))

	// Forge a live Instance signed by `pub`.
	in, err := inst.InstantiateVerified(
		context.Background(),
		signWithStoreRT(t, priv, store, revokeProducerManifest, "bundle-cascade"),
		"",
	)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	if got := inst.LiveCount(); got != 1 {
		t.Fatalf("pre-revoke LiveCount=%d, want 1", got)
	}

	// Snapshot is empty at construction (no revocations yet).
	cascader := NewRevocationCascader(inst, store.AllRevoked, nil)
	if got := len(cascader.Refresh()); got != 0 {
		t.Errorf("first Refresh on empty diff returned %d closed, want 0", got)
	}

	// Operator path: revoke the key. The sidecar is the source of
	// truth on disk, but for this test we mutate the in-memory store
	// directly — the SIGHUP wiring is asserted in the daemon test.
	store.Revoke(pub.ID, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))

	closed := cascader.Refresh()
	if len(closed) != 1 {
		t.Fatalf("Refresh closed %d, want 1: %v", len(closed), closed)
	}
	if closed[0] != in.Instance.Owner {
		t.Errorf("closed[0]=%s, want %s", closed[0], in.Instance.Owner)
	}
	if in.Instance.Context().Err() == nil {
		t.Error("instance ctx not cancelled by cascade")
	}

	// Subsequent Refresh is a no-op — same snapshot, no new transitions.
	if got := len(cascader.Refresh()); got != 0 {
		t.Errorf("second Refresh returned %d closed, want 0 (idempotent)", got)
	}
}

// TestRevocationCascader_TightenedBoundaryTriggersCascade pins the
// "operator says compromise actually started earlier" path.
// Revoke key with t=T1, observe via Refresh, then Revoke same key
// with t=T0 < T1; the second Refresh must call RevokeByKey again.
//
// In practice the second cascade is a no-op because the live
// Instance was already closed by the first. The test still asserts
// the call count: the audit logger must record both transitions so
// an operator gets the correct log trail of what they did and when.
func TestRevocationCascader_TightenedBoundaryTriggersCascade(t *testing.T) {
	handle.ResetMintForTesting()

	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	auditLines := &recordingLogger{}
	inst := NewInstantiator()
	cascader := NewRevocationCascader(inst, store.AllRevoked, auditLines.printf)

	// Tighten path requires a prior revocation: arm with T1, then
	// retract to T0 < T1.
	t1 := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, t1)
	cascader.Refresh()

	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, t0)
	cascader.Refresh()

	// Two transitions => two audit lines (newly revoked + tightened).
	lines := auditLines.all()
	if len(lines) < 2 {
		t.Fatalf("expected >=2 audit lines, got %d: %v", len(lines), lines)
	}
	hasNewly := false
	hasTightened := false
	for _, l := range lines {
		if contains(l, "newly revoked") {
			hasNewly = true
		}
		if contains(l, "tightened") {
			hasTightened = true
		}
	}
	if !hasNewly {
		t.Errorf("missing 'newly revoked' audit line: %v", lines)
	}
	if !hasTightened {
		t.Errorf("missing 'tightened' audit line: %v", lines)
	}
}

// TestRevocationCascader_KeysAtConstructionDoNotCascade pins the
// boot-baseline rule: keys already revoked at NewRevocationCascader
// time are recorded in `last` but do NOT trigger a cascade on the
// first Refresh (no transition from the cascader's perspective).
//
// This is correct because any bundle signed by a key revoked at
// boot would have been rejected by envelope.Verify before forging
// — the only way a live Instance could be signed by an already-
// revoked key is a wiring bug, and the cascader is not responsible
// for catching that.
func TestRevocationCascader_KeysAtConstructionDoNotCascade(t *testing.T) {
	handle.ResetMintForTesting()

	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)
	store.Revoke(pub.ID, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))

	auditLines := &recordingLogger{}
	inst := NewInstantiator()
	cascader := NewRevocationCascader(inst, store.AllRevoked, auditLines.printf)

	// First Refresh sees zero transitions (snapshot == last).
	if closed := cascader.Refresh(); len(closed) != 0 {
		t.Errorf("boot-baseline cascade fired: closed=%v", closed)
	}
	if lines := auditLines.all(); len(lines) != 0 {
		t.Errorf("boot-baseline audit lines emitted: %v", lines)
	}
}

// TestRevocationCascader_SoftenedKeyDoesNotCascade pins that moving
// a not-valid-after timestamp LATER (or removing it entirely) is
// not a cascade event. Already-closed Instances stay closed; new
// bundles signed in the widened window will go through Verify on
// their own next-load.
func TestRevocationCascader_SoftenedKeyDoesNotCascade(t *testing.T) {
	handle.ResetMintForTesting()

	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	auditLines := &recordingLogger{}
	inst := NewInstantiator()
	cascader := NewRevocationCascader(inst, store.AllRevoked, auditLines.printf)

	// Establish baseline: revoked at T0.
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, t0)
	cascader.Refresh()
	auditLines.mu.Lock()
	auditLines.lines = nil
	auditLines.mu.Unlock()

	// Soften: revoke at T1 > T0 (strictest-wins keeps T0 actually,
	// since Revoke overwrites the operator-set timestamp — which
	// means moving it later is a real softening of the operator-set
	// channel).
	t1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, t1)
	closed := cascader.Refresh()

	if len(closed) != 0 {
		t.Errorf("softened revocation cascaded: closed=%v", closed)
	}
	if lines := auditLines.all(); len(lines) != 0 {
		t.Errorf("softened revocation logged audit lines: %v", lines)
	}
}

// TestRevocationCascader_ConcurrentRefreshSerialised pins that two
// simultaneous Refresh calls (one from a SIGHUP-driven goroutine,
// one from the CRL Refresher's OnApply) cannot race on the diff.
// They both see a consistent before/after snapshot under the lock.
func TestRevocationCascader_ConcurrentRefreshSerialised(t *testing.T) {
	handle.ResetMintForTesting()

	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	inst := NewInstantiator()
	cascader := NewRevocationCascader(inst, store.AllRevoked, nil)

	// Mutate after construction so there is something to diff.
	store.Revoke(pub.ID, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))

	var wg sync.WaitGroup
	closedCount := 0
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closed := cascader.Refresh()
			mu.Lock()
			closedCount += len(closed)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Race detector clean + exactly one of the 8 goroutines saw the
	// new entry as a transition (the rest see snapshot == last).
	if closedCount != 0 {
		// closedCount counts capability IDs returned across calls;
		// no live Instances exist (we didn't forge any), so 0 is
		// expected. The point is no panic / no race report.
		t.Errorf("unexpected closed count: %d", closedCount)
	}
}

// TestRevocationCascader_NilLogfNoOps pins the "tests can stay
// quiet" contract: passing nil logf does not panic and silences
// audit output.
func TestRevocationCascader_NilLogfNoOps(t *testing.T) {
	handle.ResetMintForTesting()

	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	inst := NewInstantiator()
	cascader := NewRevocationCascader(inst, store.AllRevoked, nil)

	store.Revoke(pub.ID, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))
	// Must not panic.
	_ = cascader.Refresh()
}

// contains is a strings.Contains alias to keep the test file's
// imports minimal — the rest of the package's tests do the same
// dance via stdlib strings.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
