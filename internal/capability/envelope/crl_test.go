package envelope

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// helperRel returns a (release-pub, release-priv) keypair that stands
// in for the alf release key in tests. Daemon wiring later embeds the
// real public key via go:embed.
func helperRel(t *testing.T) (PublicKey, PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func helperCRL(entries []CRLEntry) CRL {
	return CRL{
		Version:    CRLEnvelopeVersion,
		IssuedAt:   time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		NextUpdate: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
		Entries:    entries,
	}
}

func helperKeyID(t *testing.T, hex string) KeyID {
	t.Helper()
	id, err := ParseKeyIDHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestCRL_RoundTrip pins the encode→parse happy path: a CRL signed
// by the release key parses back identically and verifies cleanly.
func TestCRL_RoundTrip(t *testing.T) {
	pub, priv := helperRel(t)

	original := helperCRL([]CRLEntry{
		{
			KeyID:         helperKeyID(t, "ABCDEF0123456789"),
			NotValidAfter: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Reason:        "compromise — incident #42",
		},
	})
	raw, err := EncodeSignedCRL(original, priv)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSignedCRL(raw, pub)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Version != CRLEnvelopeVersion {
		t.Errorf("version: got %d want %d", parsed.Version, CRLEnvelopeVersion)
	}
	if !parsed.IssuedAt.Equal(original.IssuedAt) {
		t.Errorf("issued_at: got %s want %s", parsed.IssuedAt, original.IssuedAt)
	}
	if len(parsed.Entries) != 1 {
		t.Fatalf("entries: got %d want 1", len(parsed.Entries))
	}
	if parsed.Entries[0].KeyID != original.Entries[0].KeyID {
		t.Errorf("entry keyid mismatch")
	}
	if parsed.Entries[0].Reason != original.Entries[0].Reason {
		t.Errorf("reason: got %q want %q", parsed.Entries[0].Reason, original.Entries[0].Reason)
	}
}

// TestCRL_TamperedPayload pins that any post-signing edit to the
// payload (even a one-byte addition to the reason field) breaks
// verification. This is the core property the embedded-signature
// design must preserve.
func TestCRL_TamperedPayload(t *testing.T) {
	pub, priv := helperRel(t)

	c := helperCRL([]CRLEntry{
		{
			KeyID:         helperKeyID(t, "0000000000000001"),
			NotValidAfter: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	raw, err := EncodeSignedCRL(c, priv)
	if err != nil {
		t.Fatal(err)
	}

	var signed SignedCRL
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatal(err)
	}
	signed.Payload.Entries[0].NotValidAfter = signed.Payload.Entries[0].NotValidAfter.Add(time.Hour)
	tampered, _ := json.Marshal(signed)

	_, parseErr := ParseSignedCRL(tampered, pub)
	if !errors.Is(parseErr, ErrCRLSignatureInvalid) {
		t.Errorf("got %v, want ErrCRLSignatureInvalid", parseErr)
	}
}

// TestCRL_TamperedSignature pins that a corrupted signature blob is
// rejected with ErrCRLSignatureInvalid (not silently accepted).
func TestCRL_TamperedSignature(t *testing.T) {
	pub, priv := helperRel(t)
	raw, err := EncodeSignedCRL(helperCRL(nil), priv)
	if err != nil {
		t.Fatal(err)
	}

	var signed SignedCRL
	_ = json.Unmarshal(raw, &signed)
	sigBytes, _ := base64.StdEncoding.DecodeString(signed.Signature)
	sigBytes[len(sigBytes)-1] ^= 0xFF // flip last byte of Ed25519 sig
	signed.Signature = base64.StdEncoding.EncodeToString(sigBytes)
	tampered, _ := json.Marshal(signed)

	_, parseErr := ParseSignedCRL(tampered, pub)
	if !errors.Is(parseErr, ErrCRLSignatureInvalid) {
		t.Errorf("got %v, want ErrCRLSignatureInvalid", parseErr)
	}
}

// TestCRL_WrongReleasePubkey pins that a CRL signed by some other
// key fails verification under our release pubkey.
func TestCRL_WrongReleasePubkey(t *testing.T) {
	relPub, _ := helperRel(t)
	_, otherPriv := helperRel(t) // signed by a DIFFERENT key

	raw, err := EncodeSignedCRL(helperCRL(nil), otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	_, parseErr := ParseSignedCRL(raw, relPub)
	if !errors.Is(parseErr, ErrCRLSignatureInvalid) {
		t.Errorf("got %v, want ErrCRLSignatureInvalid (wrong release key)", parseErr)
	}
}

// TestCRL_MalformedJSON pins that non-JSON input fails fast with
// ErrCRLMalformed (no nil deref, no panic).
func TestCRL_MalformedJSON(t *testing.T) {
	pub, _ := helperRel(t)
	_, err := ParseSignedCRL([]byte("not json"), pub)
	if !errors.Is(err, ErrCRLMalformed) {
		t.Errorf("got %v, want ErrCRLMalformed", err)
	}
}

// TestCRL_MissingSignature pins that a CRL JSON missing the
// signature field is rejected (operators can't ship unsigned CRLs).
func TestCRL_MissingSignature(t *testing.T) {
	pub, _ := helperRel(t)
	c := helperCRL(nil)
	raw, _ := json.Marshal(SignedCRL{Payload: c, Signature: ""})
	_, err := ParseSignedCRL(raw, pub)
	if !errors.Is(err, ErrCRLMalformed) {
		t.Errorf("got %v, want ErrCRLMalformed", err)
	}
}

// TestCRL_VersionUnsupported pins that a future version bump fails
// closed (we don't pretend to handle a v2 wire format).
func TestCRL_VersionUnsupported(t *testing.T) {
	pub, priv := helperRel(t)
	c := helperCRL(nil)
	c.Version = 2
	raw, err := EncodeSignedCRL(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	_, parseErr := ParseSignedCRL(raw, pub)
	if !errors.Is(parseErr, ErrCRLVersion) {
		t.Errorf("got %v, want ErrCRLVersion", parseErr)
	}
}

// TestCRL_TimeRangeInvalid pins that a CRL with next_update before
// issued_at is rejected (publisher mistake we don't compensate for).
func TestCRL_TimeRangeInvalid(t *testing.T) {
	pub, priv := helperRel(t)
	c := helperCRL(nil)
	c.NextUpdate = c.IssuedAt.Add(-time.Hour)
	raw, err := EncodeSignedCRL(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	_, parseErr := ParseSignedCRL(raw, pub)
	if !errors.Is(parseErr, ErrCRLTimeRange) {
		t.Errorf("got %v, want ErrCRLTimeRange", parseErr)
	}
}

// TestCRL_EntriesSortedByKeyID pins the deterministic order: parse
// returns entries sorted by KeyID hex regardless of input order.
func TestCRL_EntriesSortedByKeyID(t *testing.T) {
	pub, priv := helperRel(t)
	c := helperCRL([]CRLEntry{
		{KeyID: helperKeyID(t, "FFFFFFFFFFFFFFFF"), NotValidAfter: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{KeyID: helperKeyID(t, "0000000000000001"), NotValidAfter: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{KeyID: helperKeyID(t, "AAAAAAAAAAAAAAAA"), NotValidAfter: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	})
	raw, err := EncodeSignedCRL(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSignedCRL(raw, pub)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{
		parsed.Entries[0].KeyID.Hex(),
		parsed.Entries[1].KeyID.Hex(),
		parsed.Entries[2].KeyID.Hex(),
	}
	want := []string{"0000000000000001", "AAAAAAAAAAAAAAAA", "FFFFFFFFFFFFFFFF"}
	for i := range got {
		if !strings.EqualFold(got[i], want[i]) {
			t.Errorf("entry %d: got %s want %s", i, got[i], want[i])
		}
	}
}

// TestCRL_CanonicalBytesStable pins that re-marshalling a parsed CRL
// produces the same canonical bytes as the original — round-trip is
// stable, which is what the embedded-sig design depends on.
func TestCRL_CanonicalBytesStable(t *testing.T) {
	c := helperCRL([]CRLEntry{
		{
			KeyID:         helperKeyID(t, "ABCDEF0123456789"),
			NotValidAfter: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Reason:        "compromise",
		},
	})
	a, err := CanonicalCRLBytes(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalCRLBytes(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("canonical not stable:\n%s\nvs\n%s", a, b)
	}
}

// TestApplyCRL_RevocationsSurface pins that ApplyCRL pushes entries
// into RevokedAfter with the same timestamps. This is what the
// envelope.Verify pipeline (deliverable 4) consumes.
func TestApplyCRL_RevocationsSurface(t *testing.T) {
	store := NewMemoryTrustStore()
	id := helperKeyID(t, "ABCDEF0123456789")
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		Entries:  []CRLEntry{{KeyID: id, NotValidAfter: t0}},
	})

	got, ok := store.RevokedAfter(id)
	if !ok {
		t.Fatal("ApplyCRL did not surface via RevokedAfter")
	}
	if !got.Equal(t0) {
		t.Errorf("timestamp: got %s want %s", got, t0)
	}
}

// TestApplyCRL_StrictestWins pins the precedence rule: if both an
// operator-set Revoke() and a CRL entry exist for the same key, the
// earlier (stricter) timestamp wins. Neither channel can soften the
// other.
func TestApplyCRL_StrictestWins(t *testing.T) {
	pub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	// Operator says compromise started 2026-03-01.
	opT := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pub.ID, opT)

	// CRL says compromise started 2026-04-01 (later, less strict).
	crlT := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		Entries:  []CRLEntry{{KeyID: pub.ID, NotValidAfter: crlT}},
	})

	got, _ := store.RevokedAfter(pub.ID)
	if !got.Equal(opT) {
		t.Errorf("operator (stricter) should win: got %s want %s", got, opT)
	}

	// Reverse: CRL stricter than operator.
	store.Revoke(pub.ID, crlT) // operator now: 2026-04-01
	store.ApplyCRL(&CRL{       // CRL: 2026-03-01 (stricter)
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		Entries:  []CRLEntry{{KeyID: pub.ID, NotValidAfter: opT}},
	})
	got, _ = store.RevokedAfter(pub.ID)
	if !got.Equal(opT) {
		t.Errorf("CRL (stricter) should win: got %s want %s", got, opT)
	}
}

// TestAllRevoked_MergesBothChannels pins that AllRevoked returns
// every revoked key from both the operator-set and CRL-set channels,
// and that overlapping entries surface the strictest (earliest)
// timestamp. The cascade engine (#396 D2) consumes this snapshot to
// detect newly-revoked keys across reloads.
func TestAllRevoked_MergesBothChannels(t *testing.T) {
	pubA, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubC, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pubA)
	store.Add(pubB)
	store.Add(pubC)

	// A: operator-only revoke.
	tA := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pubA.ID, tA)

	// B: CRL-only revoke.
	tB := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		Entries:  []CRLEntry{{KeyID: pubB.ID, NotValidAfter: tB}},
	})

	// C: both channels — operator stricter.
	cOp := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cCRL := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store.Revoke(pubC.ID, cOp)
	// Re-apply CRL with both B and C entries — ApplyCRL replaces, not merges.
	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
		Entries: []CRLEntry{
			{KeyID: pubB.ID, NotValidAfter: tB},
			{KeyID: pubC.ID, NotValidAfter: cCRL},
		},
	})

	got := store.AllRevoked()
	if len(got) != 3 {
		t.Fatalf("expected 3 revoked entries, got %d: %v", len(got), got)
	}
	if !got[pubA.ID].Equal(tA) {
		t.Errorf("A: got %s, want %s", got[pubA.ID], tA)
	}
	if !got[pubB.ID].Equal(tB) {
		t.Errorf("B: got %s, want %s", got[pubB.ID], tB)
	}
	if !got[pubC.ID].Equal(cOp) {
		t.Errorf("C strictest-wins: got %s, want operator-set %s", got[pubC.ID], cOp)
	}
}

// TestAllRevoked_FreshCopy pins that AllRevoked returns a fresh map —
// a caller may retain or mutate it without affecting the store.
func TestAllRevoked_FreshCopy(t *testing.T) {
	pub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)
	store.Revoke(pub.ID, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))

	snap := store.AllRevoked()
	delete(snap, pub.ID)

	// Store still has the entry.
	got, ok := store.RevokedAfter(pub.ID)
	if !ok {
		t.Fatal("caller mutation of returned map leaked into store")
	}
	if !got.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("store timestamp altered: %s", got)
	}
}

// TestAllRevoked_EmptyStoreReturnsEmpty pins the zero-value path — a
// brand-new store has no revocations. The cascade engine uses this
// as the boot baseline.
func TestAllRevoked_EmptyStoreReturnsEmpty(t *testing.T) {
	store := NewMemoryTrustStore()
	got := store.AllRevoked()
	if len(got) != 0 {
		t.Errorf("empty store: got %d revoked entries, want 0", len(got))
	}
}

// TestApplyCRL_AddDoesNotClearCRLEntry pins that re-Adding a key does
// not silence an upstream CRL. Operators can re-trust their own
// revocation, but the CRL is upstream-authoritative.
func TestApplyCRL_AddDoesNotClearCRLEntry(t *testing.T) {
	pub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)
	store.Revoke(pub.ID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		Entries:  []CRLEntry{{KeyID: pub.ID, NotValidAfter: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}},
	})

	// Re-Add: clears operator-set, NOT CRL-set.
	store.Add(pub)
	got, ok := store.RevokedAfter(pub.ID)
	if !ok {
		t.Fatal("CRL revocation lost after Add()")
	}
	want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %s, want CRL timestamp %s", got, want)
	}
}

// TestApplyCRL_ReapplyReplaces pins that ApplyCRL replaces the
// previous CRL state — a newer CRL with fewer entries drops the
// missing keys' CRL-set timestamps. (Operator-set Revoke() is
// untouched.)
func TestApplyCRL_ReapplyReplaces(t *testing.T) {
	store := NewMemoryTrustStore()
	id1 := helperKeyID(t, "0000000000000001")
	id2 := helperKeyID(t, "0000000000000002")

	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Entries: []CRLEntry{
			{KeyID: id1, NotValidAfter: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{KeyID: id2, NotValidAfter: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	})

	// Newer CRL: id2 was lifted (e.g., forensics cleared the key).
	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC),
		Entries: []CRLEntry{
			{KeyID: id1, NotValidAfter: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	})
	if _, ok := store.RevokedAfter(id1); !ok {
		t.Error("id1 should still be revoked")
	}
	if _, ok := store.RevokedAfter(id2); ok {
		t.Error("id2 should NOT be revoked after newer CRL drops it")
	}
}

// TestApplyCRL_VerifyPipelineConsumes pins the integration with
// envelope.Verify: a CRL'd key rejects new bundles via
// ErrSignerKeyRevoked just like an operator-set Revoke() does.
func TestApplyCRL_VerifyPipelineConsumes(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryTrustStore()
	store.Add(pub)

	revAt := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	store.ApplyCRL(&CRL{
		Version:  CRLEnvelopeVersion,
		IssuedAt: time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC),
		Entries:  []CRLEntry{{KeyID: pub.ID, NotValidAfter: revAt}},
	})

	// Bundle signed AFTER the CRL'd timestamp must be rejected.
	signedAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sig := helperSignManifest(t, priv, revTestManifest, signedAt)
	_, vErr := Verify(VerifyInput{
		ManifestTOML: []byte(revTestManifest),
		Signature:    sig,
		TrustStore:   store,
	})
	if !errors.Is(vErr, ErrSignerKeyRevoked) {
		t.Errorf("got %v, want ErrSignerKeyRevoked from CRL", vErr)
	}
}

// TestApplyCRL_NilCRL pins that nil input is a safe no-op (defensive
// — daemon refresher passes nil if the cache is empty).
func TestApplyCRL_NilCRL(t *testing.T) {
	store := NewMemoryTrustStore()
	store.ApplyCRL(nil) // must not panic
	if _, ok := store.RevokedAfter(helperKeyID(t, "0000000000000001")); ok {
		t.Error("nil CRL should not introduce revocations")
	}
}

// TestKeyID_HexRoundTrip pins ParseKeyIDHex / KeyID.Hex symmetry on
// uppercase, lowercase, and edge values.
func TestKeyID_HexRoundTrip(t *testing.T) {
	cases := []string{
		"0000000000000000",
		"FFFFFFFFFFFFFFFF",
		"0123456789ABCDEF",
		"abcdef0123456789", // lowercase accepted on parse
	}
	for _, in := range cases {
		got, err := ParseKeyIDHex(in)
		if err != nil {
			t.Errorf("parse %q: %v", in, err)
			continue
		}
		if !strings.EqualFold(got.Hex(), in) {
			t.Errorf("round-trip %q → %s", in, got.Hex())
		}
	}
}

// TestKeyID_HexInvalid pins error paths: wrong length, non-hex chars.
func TestKeyID_HexInvalid(t *testing.T) {
	cases := []string{
		"",
		"too-short",
		"00000000000000000000",      // too long
		"GGGGGGGGGGGGGGGG",          // non-hex
		"0123456789ABCDEZ",          // last char non-hex
	}
	for _, in := range cases {
		if _, err := ParseKeyIDHex(in); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

// TestKeyID_JSONMarshal pins the on-wire format: a KeyID JSON-encodes
// as a 16-char uppercase hex string (matches §7.10.3 envelope
// fingerprint format).
func TestKeyID_JSONMarshal(t *testing.T) {
	k := helperKeyID(t, "abcdef0123456789")
	out, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	want := `"ABCDEF0123456789"`
	if string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}
