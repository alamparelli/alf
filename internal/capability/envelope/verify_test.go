package envelope

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// signValidBundle generates a signer, signs the canonical manifest,
// embeds bundle_sha256 + signed_at in the trusted comment (via the
// typed TrustedComment helper so tests exercise the production format),
// and returns all the pieces a Verify call needs.
func signValidBundle(t *testing.T, manifest string, bundle []byte) (VerifyInput, *MemoryTrustStore, PublicKey, PrivateKey) {
	t.Helper()

	pub, priv := mustGenKey(t)
	store := NewMemoryTrustStore()
	store.Add(pub)

	canonical, err := Canonicalize([]byte(manifest))
	if err != nil {
		t.Fatalf("Canonicalize setup: %v", err)
	}
	sig, err := Sign(priv, canonical)
	if err != nil {
		t.Fatalf("Sign setup: %v", err)
	}

	tc := TrustedComment{
		BundleID: "hello-read@0.1.0",
		SignedAt: time.Date(2026, 4, 24, 15, 30, 0, 0, time.UTC),
	}
	if bundle != nil {
		tc.BundleHash = sha256hex(bundle)
	}
	sigFile, err := EncodeSignatureFile(priv, sig, BuildTrustedComment(tc))
	if err != nil {
		t.Fatalf("EncodeSignatureFile setup: %v", err)
	}

	return VerifyInput{
		ManifestTOML: []byte(manifest),
		Signature:    sigFile,
		Bundle:       bundle,
		TrustStore:   store,
	}, store, pub, priv
}

func TestVerify_HappyPath(t *testing.T) {
	in, _, pub, _ := signValidBundle(t, validManifest(), []byte("wasm-bytes"))
	vm, err := Verify(in)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if vm.Manifest.ID != "hello-read" {
		t.Errorf("manifest ID=%q", vm.Manifest.ID)
	}
	if vm.SignerID != pub.ID {
		t.Errorf("signer ID mismatch")
	}
	if len(vm.CanonicalBytes) == 0 {
		t.Error("canonical bytes not returned")
	}
}

func TestVerify_NoTrustStoreRejected(t *testing.T) {
	in, _, _, _ := signValidBundle(t, validManifest(), nil)
	in.TrustStore = nil
	_, err := Verify(in)
	if err == nil || !strings.Contains(err.Error(), "TrustStore") {
		t.Fatalf("nil trust store must be rejected, got %v", err)
	}
}

func TestVerify_UntrustedSignerRejected(t *testing.T) {
	in, _, pub, _ := signValidBundle(t, validManifest(), nil)
	emptyStore := NewMemoryTrustStore()
	in.TrustStore = emptyStore
	_, err := Verify(in)
	if !errors.Is(err, ErrSignerNotTrusted) {
		t.Fatalf("want ErrSignerNotTrusted, got %v", err)
	}
	if !strings.Contains(err.Error(), pub.ID.Hex()) {
		t.Errorf("error should name the rejected key %s, got %v", pub.ID.Hex(), err)
	}
}

func TestVerify_InvalidManifestRejected(t *testing.T) {
	// Sign a manifest that is syntactically valid TOML but fails the
	// schema (missing envelope version). The crypto signature passes;
	// Validate rejects it.
	invalidManifest := `id = "hello-read"
kind = "wasm-tool"
version = "0.1.0"
name = "Hello Read"
`
	in, _, _, _ := signValidBundle(t, invalidManifest, nil)
	_, err := Verify(in)
	if !errors.Is(err, ErrEnvelopeVersionMissing) {
		t.Fatalf("want ErrEnvelopeVersionMissing, got %v", err)
	}
}

func TestVerify_TamperedManifestRejected(t *testing.T) {
	// Setup with one manifest, then swap in a different one — the
	// canonical hash changes, signature no longer validates.
	in, _, _, _ := signValidBundle(t, validManifest(), nil)

	tamperedManifest := strings.Replace(validManifest(), `"0.1.0"`, `"9.9.9"`, 1)
	in.ManifestTOML = []byte(tamperedManifest)

	_, err := Verify(in)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid on tampered manifest, got %v", err)
	}
}

func TestVerify_BundleHashMissingRejected(t *testing.T) {
	// Sign with a comment that DOESN'T embed bundle_sha256=... then try
	// to Verify with a non-nil Bundle. signed_at is still required —
	// ParseTrustedComment enforces it independently.
	pub, priv := mustGenKey(t)
	store := NewMemoryTrustStore()
	store.Add(pub)

	canonical, _ := Canonicalize([]byte(validManifest()))
	sig, _ := Sign(priv, canonical)
	tc := TrustedComment{
		BundleID: "no-bundle-hash",
		SignedAt: time.Date(2026, 4, 24, 15, 30, 0, 0, time.UTC),
	}
	sigFile, _ := EncodeSignatureFile(priv, sig, BuildTrustedComment(tc))

	in := VerifyInput{
		ManifestTOML: []byte(validManifest()),
		Signature:    sigFile,
		Bundle:       []byte("wasm-bytes"),
		TrustStore:   store,
	}
	_, err := Verify(in)
	if !errors.Is(err, ErrBundleHashMissing) {
		t.Fatalf("want ErrBundleHashMissing, got %v", err)
	}
}

func TestVerify_BundleHashMismatchRejected(t *testing.T) {
	in, _, _, _ := signValidBundle(t, validManifest(), []byte("original-bytes"))

	// Swap the Bundle in-memory after signing — hash no longer matches.
	in.Bundle = []byte("tampered-bytes")

	_, err := Verify(in)
	if !errors.Is(err, ErrBundleHashMismatch) {
		t.Fatalf("want ErrBundleHashMismatch, got %v", err)
	}
}

func TestVerify_MalformedSignatureFileRejected(t *testing.T) {
	in, _, _, _ := signValidBundle(t, validManifest(), nil)
	in.Signature = []byte("not a minisign file")
	_, err := Verify(in)
	if !errors.Is(err, ErrSigFileMalformed) {
		t.Fatalf("want ErrSigFileMalformed, got %v", err)
	}
}

func TestVerify_NilBundleSkipsHashCheck(t *testing.T) {
	// A capability kind that has no executable artefact (skill, future
	// reserved kinds) passes Bundle=nil — verify must tolerate that and
	// skip the hash cross-check.
	in, _, _, _ := signValidBundle(t, validManifest(), nil)
	in.Bundle = nil
	_, err := Verify(in)
	if err != nil {
		t.Fatalf("nil Bundle must be accepted, got %v", err)
	}
}

func TestVerify_ReturnsSignerIDForAudit(t *testing.T) {
	in, _, pub, _ := signValidBundle(t, validManifest(), nil)
	vm, err := Verify(in)
	if err != nil {
		t.Fatal(err)
	}
	if vm.SignerID != pub.ID {
		t.Errorf("SignerID=%s, want %s", vm.SignerID.Hex(), pub.ID.Hex())
	}
}

// (TestExtractBundleHash removed — functionality replaced by the typed
// TrustedComment parser; see comment_test.go for coverage.)
