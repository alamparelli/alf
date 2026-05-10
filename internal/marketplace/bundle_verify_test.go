package marketplace

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// signedMarketplaceBundle assembles a fixture bundle for the
// verify-path tests: a TOML manifest declaring kind="marketplace-app"
// (or "wasm-app") inside a ZIP, plus a detached minisign signature
// computed over the canonical TOML and binding the bundle's SHA-256
// hash via the trusted comment.
//
// Returns the bundle bytes, the detached signature bytes, and the
// trust store with the publisher key registered. Caller passes the
// store to verifyBundle so the trust-chain check passes.
func signedMarketplaceBundle(t *testing.T, kind, slug string) (bundleBytes, sigBytes []byte, store *envelope.MemoryTrustStore) {
	t.Helper()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	manifestTOML := []byte("alf_envelope_version = 1\n" +
		"id      = \"" + slug + "\"\n" +
		"kind    = \"" + kind + "\"\n" +
		"version = \"0.1.0\"\n" +
		"name    = \"Marketplace fixture\"\n")

	// Build the ZIP first so the trusted comment can carry its hash.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	{
		fw, err := zw.Create("manifest.toml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(manifestTOML); err != nil {
			t.Fatal(err)
		}
		// Sibling artefact so the test exercises a "real" multi-file bundle.
		other, err := zw.Create("index.html")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Write([]byte("<html>fixture</html>")); err != nil {
			t.Fatal(err)
		}
		// manifest.json is the legacy view the activate() path consumes
		// to populate Manager.perms / Manager.services. The marketplace
		// bundle ships BOTH formats during the v0.8.0 migration window;
		// the envelope (manifest.toml) is the trust gate, manifest.json
		// is the runtime metadata source until #414 retires the legacy
		// activate path.
		js, err := zw.Create("manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := js.Write([]byte(`{"slug":"` + slug + `","name":"` + slug + `","version":"0.1.0"}`)); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	bundleBytes = append([]byte(nil), buf.Bytes()...)

	// Sign the canonicalised manifest bytes.
	canonical, err := envelope.Canonicalize(manifestTOML)
	if err != nil {
		t.Fatal(err)
	}
	sigBlob, err := envelope.Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}

	// Trusted comment carries bundle hash so envelope.Verify's
	// BundleSHA256 cross-check matches.
	hash := sha256.Sum256(bundleBytes)
	tc := envelope.TrustedComment{
		BundleID:   slug,
		SignedAt:   time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		BundleHash: hex.EncodeToString(hash[:]),
	}
	sigBytes, err = envelope.EncodeSignatureFile(priv, sigBlob, envelope.BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}

	store = envelope.NewMemoryTrustStore()
	store.Add(pub)
	return bundleBytes, sigBytes, store
}

// TestVerifyBundle_RejectsMarketplaceAppKind pins the §4.1 lockdown
// (#420): even when a marketplace-app envelope is otherwise validly
// signed by a trusted key with a matching bundle hash, the install
// path refuses it. The legacy "marketplace-app" kind is retired per
// MANIFEST-SCHEMA.md §3.3 and replaced by "wasm-app".
//
// Pre-lockdown this test asserted the happy path. Flipping it to a
// rejection test is the structural delta — the parser still accepts
// the kind for fixture compatibility, but no install path admits it.
func TestVerifyBundle_RejectsMarketplaceAppKind(t *testing.T) {
	bundle, sig, store := signedMarketplaceBundle(t, "marketplace-app", "fixture-app")
	_, err := verifyBundle(bundle, sig, store)
	if err == nil {
		t.Fatalf("verifyBundle: expected ErrBundleManifestNotMarketplace, got nil")
	}
	if !errors.Is(err, ErrBundleManifestNotMarketplace) {
		t.Fatalf("verifyBundle: expected ErrBundleManifestNotMarketplace, got %v", err)
	}
}

// TestVerifyBundle_AcceptsWASMAppKind pins that the new wasm-app
// envelope is also accepted on the marketplace path. Per
// MANIFEST-SCHEMA §4.6 the two kinds share an identical shape and
// the marketplace migration is in flight; refusing wasm-app would
// block server-side adoption of the post-deprecation kind.
func TestVerifyBundle_AcceptsWASMAppKind(t *testing.T) {
	bundle, sig, store := signedMarketplaceBundle(t, "wasm-app", "wasm-fixture")
	man, err := verifyBundle(bundle, sig, store)
	if err != nil {
		t.Fatalf("verifyBundle: %v", err)
	}
	if man.Kind != envelope.KindWASMApp {
		t.Errorf("Kind: got %q, want %q", man.Kind, envelope.KindWASMApp)
	}
}

// TestVerifyBundle_RejectsOtherKinds pins that an envelope of any
// kind beyond marketplace-app / wasm-app is rejected at the
// marketplace boundary even if its signature is valid. This guards
// against an attacker (or a confused server upgrade) shipping a
// "skill" or "wasm-tool" through the marketplace install surface.
func TestVerifyBundle_RejectsOtherKinds(t *testing.T) {
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	manifestTOML := []byte("alf_envelope_version = 1\n" +
		"id      = \"some-skill\"\n" +
		"kind    = \"skill\"\n" +
		"version = \"0.1.0\"\n" +
		"name    = \"Wrong kind\"\n")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, _ := zw.Create("manifest.toml")
	fw.Write(manifestTOML)
	zw.Close()
	bundle := buf.Bytes()

	canonical, err := envelope.Canonicalize(manifestTOML)
	if err != nil {
		t.Fatal(err)
	}
	sigBlob, err := envelope.Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(bundle)
	tc := envelope.TrustedComment{
		BundleID:   "some-skill",
		SignedAt:   time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		BundleHash: hex.EncodeToString(hash[:]),
	}
	sig, err := envelope.EncodeSignatureFile(priv, sigBlob, envelope.BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	if _, err := verifyBundle(bundle, sig, store); !errors.Is(err, ErrBundleManifestNotMarketplace) {
		t.Errorf("got %v, want ErrBundleManifestNotMarketplace", err)
	}
}

// TestVerifyBundle_RejectsTamperedBundle pins that flipping a single
// byte of the bundle (after signing) breaks the BundleHash
// cross-check inside envelope.Verify. The tampered bundle either
// fails signature verification (if the manifest itself moved) or
// the bundle-hash check (if the tampering was outside manifest.toml
// but inside the ZIP). Both surface as a non-nil error from
// envelope.Verify, which verifyBundle wraps and returns.
func TestVerifyBundle_RejectsTamperedBundle(t *testing.T) {
	bundle, sig, store := signedMarketplaceBundle(t, "marketplace-app", "fixture-app")

	// Flip a byte WELL inside the ZIP payload (not in the central
	// directory header which would corrupt the ZIP entirely).
	tampered := append([]byte(nil), bundle...)
	if len(tampered) < 80 {
		t.Fatalf("bundle unexpectedly small: %d bytes", len(tampered))
	}
	tampered[60] ^= 0xff

	if _, err := verifyBundle(tampered, sig, store); err == nil {
		t.Error("tampered bundle accepted")
	}
}

// TestVerifyBundle_RejectsUnknownKey pins that a bundle signed by
// a key NOT in the trust store fails verification with
// ErrSignerNotTrusted, even if the signature itself is otherwise
// well-formed. This is the "redirect to alf trust add" UX gate
// from #384's acceptance.
func TestVerifyBundle_RejectsUnknownKey(t *testing.T) {
	bundle, sig, _ := signedMarketplaceBundle(t, "marketplace-app", "fixture-app")
	emptyStore := envelope.NewMemoryTrustStore()

	_, err := verifyBundle(bundle, sig, emptyStore)
	if !errors.Is(err, envelope.ErrSignerNotTrusted) {
		t.Errorf("got %v, want ErrSignerNotTrusted", err)
	}
}

// TestVerifyBundle_RejectsBundleWithoutManifestTOML pins that a
// ZIP without a top-level manifest.toml — typical of pre-v0.8.0
// marketplace bundles — is rejected with the typed error so the
// daemon can surface a "registry not yet upgraded" message.
func TestVerifyBundle_RejectsBundleWithoutManifestTOML(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, _ := zw.Create("manifest.json")
	fw.Write([]byte(`{"slug":"legacy"}`))
	zw.Close()
	legacyBundle := buf.Bytes()

	_, err := verifyBundle(legacyBundle, nil, envelope.NewMemoryTrustStore())
	if !errors.Is(err, ErrBundleManifestMissing) {
		t.Errorf("got %v, want ErrBundleManifestMissing", err)
	}
}

// TestReadManifestFromZip_RejectsOversizedManifest pins the
// memory-bomb defence: a manifest.toml larger than
// MaxBundleManifestSize is refused even if the rest of the ZIP is
// valid. Bound is deliberately small (64 KiB) so a hostile
// "manifest" cannot be used to spool gigabytes into the daemon
// before envelope.Verify reads it.
func TestReadManifestFromZip_RejectsOversizedManifest(t *testing.T) {
	huge := bytes.Repeat([]byte("a"), MaxBundleManifestSize+1)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, _ := zw.Create("manifest.toml")
	fw.Write(huge)
	zw.Close()

	_, err := readManifestFromZip(buf.Bytes())
	if err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
