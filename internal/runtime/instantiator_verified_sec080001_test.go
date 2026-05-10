package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// SEC-080-001: the gap between envelope.Verify (which consults the
// trust store once) and trackLive (which adds the Instance to the
// live registry that RevokeByKey scans) is non-atomic. A SIGHUP-
// driven trust-store Load() in that gap could mark the signer
// revoked AFTER Verify accepted but BEFORE trackLive added the
// entry, leaving a forged Instance live on a now-revoked key with no
// further discovery channel firing for it.
//
// The fix re-checks the trust store inside trackLive under liveMu;
// any concurrent RevokeByKey for the same signer either runs before
// trackLive (in which case our recheck observes the revocation and
// refuses) or after (in which case the live entry is in place and
// the cascade closes it normally).
//
// These tests use the WithAfterVerifyHookForTest seam to make the
// race deterministic: the hook fires inside InstantiateVerified
// between Verify and trackLive, simulating the SIGHUP-driven Load()
// that would arrive in the gap.

// TestInstantiateVerified_SEC080001_SignerRevokedBetweenVerifyAndTrack
// pins the direct-revocation half of the fix. The hook revokes the
// bundle's signer with a not-valid-after equal to the bundle's
// signed-at — strictly meeting the at-or-before condition the
// recheck enforces. trackLive must refuse, the Instance must be
// closed, and the live registry must be empty.
func TestInstantiateVerified_SEC080001_SignerRevokedBetweenVerifyAndTrack(t *testing.T) {
	handle.ResetMintForTesting()

	in, store := signBundle(t, verifiedManifest, []byte("wasm-bytes"))

	// First InstantiateVerified call lets us learn the signer's
	// KeyID via the returned VerifiedInstantiation. We close it
	// immediately so the live registry is empty for the racy call.
	probe := NewInstantiator()
	vi, err := probe.InstantiateVerified(context.Background(), in, "/tmp/probe")
	if err != nil {
		t.Fatalf("probe instantiate: %v", err)
	}
	signerID := vi.SignerID
	signedAt := vi.SignedAt
	vi.Instance.Close()

	// Build the racy Instantiator. The hook fires between Verify
	// and trackLive — it revokes the signer with cutoff = signedAt
	// (the recheck rule rejects when !signedAt.Before(cutoff), i.e.
	// signedAt >= cutoff).
	handle.ResetMintForTesting()
	hookFired := false
	inst := NewInstantiator(WithAfterVerifyHookForTest(func() {
		store.Revoke(signerID, signedAt)
		hookFired = true
	}))

	out, err := inst.InstantiateVerified(context.Background(), in, "/tmp/racy")
	if !errors.Is(err, ErrRevokedBetweenVerifyAndTrack) {
		t.Fatalf("expected ErrRevokedBetweenVerifyAndTrack, got %v (out=%v)", err, out)
	}
	if !hookFired {
		t.Fatal("after-verify hook did not fire")
	}
	if out != nil {
		t.Errorf("VerifiedInstantiation must be nil on rejection, got %+v", out)
	}
	if got := inst.LiveCount(); got != 0 {
		t.Errorf("live registry must be empty after refused track, got %d", got)
	}
}

// TestInstantiateVerified_SEC080001_StaysOpenWhenSignedBeforeRevoke
// pins the inverse: when the bundle was signed STRICTLY before the
// revocation cutoff, the recheck must let it through. This guards
// against an over-eager fix that would refuse legitimate bundles.
func TestInstantiateVerified_SEC080001_StaysOpenWhenSignedBeforeRevoke(t *testing.T) {
	handle.ResetMintForTesting()

	in, store := signBundle(t, verifiedManifest, []byte("wasm-bytes"))

	// Probe to learn the signer ID + signed-at.
	probe := NewInstantiator()
	vi, err := probe.InstantiateVerified(context.Background(), in, "/tmp/probe")
	if err != nil {
		t.Fatalf("probe instantiate: %v", err)
	}
	signerID := vi.SignerID
	signedAt := vi.SignedAt
	vi.Instance.Close()

	// Hook revokes the signer but with cutoff strictly AFTER
	// signed-at — the bundle predates the revocation, so the
	// recheck must permit it. Both verify and recheck use the
	// "signed-at strictly before T accepts" rule.
	handle.ResetMintForTesting()
	inst := NewInstantiator(WithAfterVerifyHookForTest(func() {
		store.Revoke(signerID, signedAt.Add(time.Hour))
	}))

	out, err := inst.InstantiateVerified(context.Background(), in, "/tmp/racy")
	if err != nil {
		t.Fatalf("legitimate older bundle rejected: %v", err)
	}
	if out == nil || out.Instance == nil {
		t.Fatal("expected VerifiedInstantiation with live Instance")
	}
	defer out.Instance.Close()
	if got := inst.LiveCount(); got != 1 {
		t.Errorf("live registry expected 1 entry, got %d", got)
	}
}

// TestInstantiateVerified_SEC080001_HookFiresAtBoundary is a behaviour
// pin: the after-verify hook seam used above must fire EXACTLY between
// envelope.Verify and trackLive. If a future refactor moves trackLive
// before the hook, the recheck in trackLive becomes a dead branch —
// the deterministic SEC080001 fix tests above would still pass, but
// the production race window would be wide open. This test exercises
// the ordering directly via the hook side-effects.
func TestInstantiateVerified_SEC080001_HookFiresAtBoundary(t *testing.T) {
	handle.ResetMintForTesting()
	in, _ := signBundle(t, verifiedManifest, []byte("wasm-bytes"))

	hookCount := 0
	inst := NewInstantiator(WithAfterVerifyHookForTest(func() {
		hookCount++
	}))
	out, err := inst.InstantiateVerified(context.Background(), in, "/tmp/probe")
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer out.Instance.Close()
	if hookCount != 1 {
		t.Errorf("after-verify hook fired %d times, want 1", hookCount)
	}
}

