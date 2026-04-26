package envelope

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

// helperSignManifest is a parameter-driven sign helper that lets
// each revocation test pin the exact signed-at timestamp embedded in
// the trusted comment. Existing signBundle helpers in other packages
// hard-code 2026-04-24, which is too coarse for boundary tests.
func helperSignManifest(t *testing.T, priv PrivateKey, manifestTOML string, signedAt time.Time) []byte {
	t.Helper()
	canonical, err := Canonicalize([]byte(manifestTOML))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}
	tc := TrustedComment{
		BundleID: "rev-test",
		SignedAt: signedAt,
	}
	// Embed a stable bundle hash so VerifyInput.Bundle can be omitted.
	h := sha256.Sum256([]byte("noop"))
	const hex = "0123456789abcdef"
	hx := make([]byte, 64)
	for i, b := range h {
		hx[i*2] = hex[b>>4]
		hx[i*2+1] = hex[b&0x0f]
	}
	tc.BundleHash = string(hx)
	sigFile, err := EncodeSignatureFile(priv, sig, BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}
	return sigFile
}

const revTestManifest = `alf_envelope_version = 1
id      = "rev-test-cap"
kind    = "wasm-tool"
version = "0.1.0"
name    = "RevTest"
`

// TestRevocation_SignedBeforeRevocation_Accepted pins the happy
// path: a bundle whose signed-at predates the revocation timestamp
// must verify successfully. The key compromise is forward-looking;
// pre-compromise bundles stay valid.
func TestRevocation_SignedBeforeRevocation_Accepted(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	signedAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	revokedAfter := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC) // 3 months later
	store.Revoke(pub.ID, revokedAfter)

	sig := helperSignManifest(t, priv, revTestManifest, signedAt)

	if _, err := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	}); err != nil {
		t.Errorf("pre-revocation bundle should verify, got: %v", err)
	}
}

// TestRevocation_SignedAfterRevocation_Rejected pins the core
// invariant: a bundle whose signed-at is past the revocation
// timestamp must be rejected with ErrSignerKeyRevoked even though
// the key is still in the trust store.
func TestRevocation_SignedAfterRevocation_Rejected(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	revokedAfter := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	signedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC) // 1 month later
	store.Revoke(pub.ID, revokedAfter)

	sig := helperSignManifest(t, priv, revTestManifest, signedAt)

	_, verifyErr := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	})
	if !errors.Is(verifyErr, ErrSignerKeyRevoked) {
		t.Errorf("got %v, want ErrSignerKeyRevoked", verifyErr)
	}

	// And the key is still in the store — distinct from "not trusted".
	if _, ok, _ := store.Lookup(pub.ID); !ok {
		t.Error("key should still be in store; revocation is time-bound, not removal")
	}
}

// TestRevocation_BoundarySignedAtEqualsRevocation_Rejected pins the
// strict-before semantics. A bundle signed AT the revocation moment
// (not strictly before) is rejected. This matches the operator
// mental model: "compromise window opens at T; nothing from T
// onward is trustworthy".
func TestRevocation_BoundarySignedAtEqualsRevocation_Rejected(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	t0 := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, t0)
	sig := helperSignManifest(t, priv, revTestManifest, t0) // exactly equal

	_, verifyErr := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	})
	if !errors.Is(verifyErr, ErrSignerKeyRevoked) {
		t.Errorf("boundary case (signed-at == revoked-at) should reject, got: %v", verifyErr)
	}
}

// TestRevocation_NoRevocationRecorded_Accepts pins that an
// untouched MemoryTrustStore (no Revoke calls) admits any signed-at.
// Backwards compatibility: existing call sites that never revoke
// keys see no behaviour change.
func TestRevocation_NoRevocationRecorded_Accepts(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	signedAt := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) // far future
	sig := helperSignManifest(t, priv, revTestManifest, signedAt)

	if _, err := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	}); err != nil {
		t.Errorf("no revocation recorded → bundle should verify, got: %v", err)
	}
}

// TestRevocation_LatestRevocationWins pins that calling Revoke
// twice on the same key replaces the timestamp. Operators who
// realise the compromise started earlier than first thought can
// shift the boundary backward; the most recent Revoke is authoritative.
func TestRevocation_LatestRevocationWins(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	// First revocation: way in the future.
	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, farFuture)

	// Tighten the boundary to "compromise actually started January 2026".
	tighter := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, tighter)

	// A bundle signed in February 2026 — would have been accepted
	// under the first revocation, must be rejected under the second.
	signedAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	sig := helperSignManifest(t, priv, revTestManifest, signedAt)

	_, verifyErr := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	})
	if !errors.Is(verifyErr, ErrSignerKeyRevoked) {
		t.Errorf("latest revocation should win and reject, got: %v", verifyErr)
	}
}

// TestRevocation_AddClearsPreviousRevocation pins that re-Adding a
// key wipes its revocation. This handles the "forensics complete,
// re-trust the key" case: operators know how to surface this in
// the admin CLI confirmation.
func TestRevocation_AddClearsPreviousRevocation(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)
	store.Revoke(pub.ID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	// Re-Add after revocation — should clear the boundary.
	store.Add(pub)

	if _, ok := store.RevokedAfter(pub.ID); ok {
		t.Error("Add() did not clear prior revocation timestamp")
	}

	// And a freshly-signed bundle verifies.
	signedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sig := helperSignManifest(t, priv, revTestManifest, signedAt)
	if _, err := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	}); err != nil {
		t.Errorf("re-Added key should verify fresh bundle, got: %v", err)
	}
}

// TestRevocation_RemoveAlsoClearsRevocation pins that Remove()
// drops the revocation timestamp alongside the key. A revocation
// timestamp without an associated key is meaningless state.
func TestRevocation_RemoveAlsoClearsRevocation(t *testing.T) {
	pub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)
	store.Revoke(pub.ID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	store.Remove(pub.ID)

	if _, ok := store.RevokedAfter(pub.ID); ok {
		t.Error("Remove() did not clear revocation timestamp")
	}
}

// TestRevocation_RevokeUnknownKeyIsNoOp pins that Revoke on a key
// not in the store does nothing — avoids dangling timestamps that
// would activate if the same KeyID is ever Add()ed.
func TestRevocation_RevokeUnknownKeyIsNoOp(t *testing.T) {
	store := NewMemoryTrustStore()
	var ghost KeyID
	for i := range ghost {
		ghost[i] = 0xff
	}

	store.Revoke(ghost, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	if _, ok := store.RevokedAfter(ghost); ok {
		t.Error("Revoke on unknown key recorded a timestamp anyway")
	}
}

// TestRevocation_TrustStoreInterfaceFallback pins backward
// compatibility: a TrustStore implementation that does NOT satisfy
// the Revoker interface (e.g., an alternate store an external
// caller might supply) bypasses the revocation check entirely. We
// don't break callers; we just don't enforce time-bound revocation
// for them.
//
// The check uses a bare TrustStore wrapper that doesn't expose the
// Revoke methods.
func TestRevocation_TrustStoreInterfaceFallback(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	inner := NewMemoryTrustStore()
	inner.Add(pub)
	inner.Revoke(pub.ID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	// Wrap inner in a type that only exposes Lookup + Keys (no
	// Revoker methods). Verify must NOT see the revocation.
	wrapped := nonRevokerStore{inner}

	signedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sig := helperSignManifest(t, priv, revTestManifest, signedAt)

	if _, err := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   wrapped,
	}); err != nil {
		t.Errorf("non-Revoker store: revocation must be ignored, got: %v", err)
	}
}

// nonRevokerStore is a TrustStore wrapper that hides the Revoker
// interface — used to pin the type-assertion fallback in Verify.
type nonRevokerStore struct{ inner *MemoryTrustStore }

func (n nonRevokerStore) Lookup(id KeyID) (PublicKey, bool, error) { return n.inner.Lookup(id) }
func (n nonRevokerStore) Keys() []KeyID                            { return n.inner.Keys() }

// TestRevocation_DistinctFromNotTrusted pins the operator-facing
// distinction: ErrSignerKeyRevoked and ErrSignerNotTrusted are
// separate error sentinels because the remediation is different.
//   - NotTrusted → install or re-trust the key
//   - KeyRevoked → key compromised; replace bundle with one signed
//     by a non-revoked key (or the same key after re-trust)
//
// The errors must NOT match each other under errors.Is.
func TestRevocation_DistinctFromNotTrusted(t *testing.T) {
	if errors.Is(ErrSignerKeyRevoked, ErrSignerNotTrusted) {
		t.Error("ErrSignerKeyRevoked should not match ErrSignerNotTrusted")
	}
	if errors.Is(ErrSignerNotTrusted, ErrSignerKeyRevoked) {
		t.Error("ErrSignerNotTrusted should not match ErrSignerKeyRevoked")
	}
}
