package runtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// signBundle generates a signer, signs the canonical manifest, and
// returns everything InstantiateVerified needs. Used by the verified
// tests — the corresponding envelope-level helper lives in the
// envelope package's own tests.
func signBundle(t *testing.T, manifestTOML string, bundle []byte) (envelope.VerifyInput, *envelope.MemoryTrustStore) {
	t.Helper()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	canonical, err := envelope.Canonicalize([]byte(manifestTOML))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := envelope.Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}

	tc := envelope.TrustedComment{
		BundleID: "test-bundle",
		SignedAt: time.Date(2026, 4, 24, 15, 30, 0, 0, time.UTC),
	}
	if bundle != nil {
		h := sha256.Sum256(bundle)
		const hex = "0123456789abcdef"
		hx := make([]byte, 64)
		for i, b := range h {
			hx[i*2] = hex[b>>4]
			hx[i*2+1] = hex[b&0x0f]
		}
		tc.BundleHash = string(hx)
	}
	sigFile, err := envelope.EncodeSignatureFile(priv, sig, envelope.BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}

	return envelope.VerifyInput{
		ManifestTOML: []byte(manifestTOML),
		Signature:    sigFile,
		Bundle:       bundle,
		TrustStore:   store,
	}, store
}

const verifiedManifest = `alf_envelope_version = 1
id      = "verified-cap"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Verified Cap"

[[fs.reads]]
path = "data/"
`

func TestInstantiateVerified_HappyPath(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator()

	in, _ := signBundle(t, verifiedManifest, []byte("wasm-bytes"))
	vi, err := inst.InstantiateVerified(context.Background(), in, "/tmp/verified-cap")
	if err != nil {
		t.Fatalf("InstantiateVerified: %v", err)
	}
	defer vi.Instance.Close()

	if vi.Instance.Owner != "verified-cap" {
		t.Errorf("Owner=%q, want verified-cap", vi.Instance.Owner)
	}
	if vi.Instance.FS == nil {
		t.Error("FS handle nil despite declared fs.reads")
	}
	if vi.Manifest == nil {
		t.Fatal("Manifest nil on success")
	}
	if vi.Manifest.ID != "verified-cap" {
		t.Errorf("Manifest.ID=%q, want verified-cap", vi.Manifest.ID)
	}
	if vi.Manifest.Kind != envelope.KindWASMTool {
		t.Errorf("Manifest.Kind=%q, want wasm-tool", vi.Manifest.Kind)
	}
	if len(vi.Manifest.FS.Reads) != 1 || vi.Manifest.FS.Reads[0].Path != "data/" {
		t.Errorf("Manifest.FS.Reads=%v, want [{data/}]", vi.Manifest.FS.Reads)
	}
}

func TestInstantiateVerified_TamperedBundleRejected(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator()

	// Sign with the real bundle bytes, then swap to different bytes
	// post-signing. The hash in the trusted comment will mismatch.
	in, _ := signBundle(t, verifiedManifest, []byte("wasm-bytes"))
	in.Bundle = []byte("tampered-wasm-bytes")

	_, err := inst.InstantiateVerified(context.Background(), in, "/tmp/x")
	if !errors.Is(err, envelope.ErrBundleHashMismatch) {
		t.Fatalf("want ErrBundleHashMismatch, got %v", err)
	}
}

func TestInstantiateVerified_UntrustedSignerRejected(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator()

	in, _ := signBundle(t, verifiedManifest, nil)
	in.TrustStore = envelope.NewMemoryTrustStore() // empty store

	_, err := inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, envelope.ErrSignerNotTrusted) {
		t.Fatalf("want ErrSignerNotTrusted, got %v", err)
	}
}

func TestInstantiateVerified_TamperedManifestRejected(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator()

	in, _ := signBundle(t, verifiedManifest, nil)
	// Swap the manifest bytes in-memory after signing — tamper with
	// content that survives canonicalisation (comments don't; the
	// version string does).
	tampered := `alf_envelope_version = 1
id      = "verified-cap"
kind    = "wasm-tool"
version = "9.9.9"
name    = "Verified Cap"

[[fs.reads]]
path = "data/"
`
	in.ManifestTOML = []byte(tampered)

	_, err := inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, envelope.ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestInstantiateVerified_InvalidManifestRejected(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator()

	badManifest := `id = "bad"
kind = "wasm-tool"
version = "0"
name = "Bad"
`
	in, _ := signBundle(t, badManifest, nil)
	_, err := inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, envelope.ErrEnvelopeVersionMissing) {
		t.Fatalf("want ErrEnvelopeVersionMissing, got %v", err)
	}
}

func TestInstantiateVerified_EmitsHandleRevocable(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator()

	in, _ := signBundle(t, verifiedManifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "/tmp/x")
	if err != nil {
		t.Fatal(err)
	}

	vi.Instance.Close()
	if _, err := vi.Instance.FS.Read(context.Background(), "data/foo"); !errors.Is(err, handle.ErrRevoked) {
		t.Errorf("FS.Read after Close: want ErrRevoked, got %v", err)
	}
}
