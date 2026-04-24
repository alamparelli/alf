package envelope

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// signValidBundle generates a signer, signs the canonical manifest,
// embeds the bundle_sha256 in the trusted comment, and returns all the
// pieces a Verify call needs. Used as a setup helper by the verify
// tests — keeps each test focused on the property under check.
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

	comment := "bundle hello-read@0.1.0"
	if bundle != nil {
		comment = fmt.Sprintf("%s bundle_sha256=%s", comment, sha256hex(bundle))
	}
	sigFile, err := EncodeSignatureFile(priv, sig, comment)
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
	// to Verify with a non-nil Bundle.
	pub, priv := mustGenKey(t)
	store := NewMemoryTrustStore()
	store.Add(pub)

	canonical, _ := Canonicalize([]byte(validManifest()))
	sig, _ := Sign(priv, canonical)
	sigFile, _ := EncodeSignatureFile(priv, sig, "bundle with no hash field")

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

func TestExtractBundleHash(t *testing.T) {
	cases := map[string]struct {
		comment string
		want    string
		ok      bool
	}{
		"at end":                 {"bundle x bundle_sha256=abc123", "abc123", true},
		"at start":               {"bundle_sha256=abc123 other field", "abc123", true},
		"in middle":              {"first bundle_sha256=abc123 last", "abc123", true},
		"absent":                 {"bundle x other y", "", false},
		"prefix-only":            {"bundle_sha256=", "", true},
		"empty":                  {"", "", false},
		"similar-field-rejected": {"other_bundle_sha256=abc other", "", false},
	}
	for name, tc := range cases {
		got, ok := extractBundleHash(tc.comment)
		// Edge case: "prefix-only" gives "" as value — len(token)=len(prefix)
		// so our >len(prefix) guard rejects it. Document that the extractor
		// requires a non-empty hash; the Verify pipeline would fail on
		// an empty hash anyway via ErrBundleHashMismatch.
		if name == "prefix-only" {
			if ok {
				t.Errorf("%s: empty-value should be treated as no field (got ok=%v val=%q)", name, ok, got)
			}
			continue
		}
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", name, got, ok, tc.want, tc.ok)
		}
	}
}
