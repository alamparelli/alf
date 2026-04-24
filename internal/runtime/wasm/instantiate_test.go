package wasm

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/runtime"
)

// signTestBundle mirrors runtime/instantiator_verified_test.signBundle
// — duplicated here to avoid pulling a _test.go dependency across
// packages (Go doesn't share _test.go symbols across package
// boundaries).
func signTestBundle(t *testing.T, manifestTOML string, bundle []byte) envelope.VerifyInput {
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
	}
}

const instantiateManifest = `alf_envelope_version = 1
id      = "test-wasm"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Test WASM"
`

const instantiateManifestWithFSReads = `alf_envelope_version = 1
id      = "test-wasm"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Test WASM"

[[fs.reads]]
path = "data/"
`

// newTestRuntime wires a fresh Instantiator + wasm.Runtime. Tests
// must ResetMintForTesting before calling this because the
// Instantiator mints the runtime token once per process.
func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	handle.ResetMintForTesting()
	inst := runtime.NewInstantiator()
	r, err := NewRuntime(context.Background(), inst)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r
}

func TestRuntime_NewRuntime_NilInstantiatorRejected(t *testing.T) {
	_, err := NewRuntime(context.Background(), nil)
	if err == nil {
		t.Fatal("want error on nil Instantiator, got nil")
	}
}

func TestRuntime_Instantiate_HappyPath_NoImports(t *testing.T) {
	r := newTestRuntime(t)

	// Guest has no imports, no _initialize — wazero will instantiate
	// it as-is (no start function to call). This isolates step 5
	// pipeline wiring from the host ABI — the full round-trip of
	// guest ↔ host lives in step 10.
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifest, wasmBytes)

	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	if mod.Instance == nil {
		t.Error("Module.Instance nil")
	}
	if mod.Manifest == nil || mod.Manifest.ID != "test-wasm" {
		t.Errorf("Module.Manifest=%+v", mod.Manifest)
	}
	if mod.Guest == nil {
		t.Error("Module.Guest nil")
	}
}

func TestRuntime_Instantiate_EmptyBundleRejected(t *testing.T) {
	r := newTestRuntime(t)

	in := signTestBundle(t, instantiateManifest, nil)
	_, err := r.Instantiate(context.Background(), in, nil, "")
	if err == nil {
		t.Fatal("want error on empty wasm bytes, got nil")
	}
}

func TestRuntime_Instantiate_UntrustedSignerRejected(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifest, wasmBytes)
	in.TrustStore = envelope.NewMemoryTrustStore() // drop the trusted signer

	_, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if !errors.Is(err, envelope.ErrSignerNotTrusted) {
		t.Fatalf("want ErrSignerNotTrusted, got %v", err)
	}
}

func TestRuntime_Instantiate_TamperedBundleRejected(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifest, wasmBytes)
	// Pass different bytes to Instantiate than what was signed.
	tampered := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01}
	in.Bundle = tampered

	_, err := r.Instantiate(context.Background(), in, tampered, "")
	if !errors.Is(err, envelope.ErrBundleHashMismatch) {
		t.Fatalf("want ErrBundleHashMismatch, got %v", err)
	}
}

func TestRuntime_Instantiate_LyingManifestRejected(t *testing.T) {
	r := newTestRuntime(t)

	// Manifest declares NO fs.reads but guest imports alf_fs_read.
	wasmBytes := buildWASMWithImports([][2]string{
		{hostModuleALF, fnAlfFSRead},
	})
	in := signTestBundle(t, instantiateManifest, wasmBytes)

	_, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if !errors.Is(err, ErrLyingManifest) {
		t.Fatalf("want ErrLyingManifest, got %v", err)
	}
}

func TestRuntime_Instantiate_FSHandleForgedFromDeclaredReads(t *testing.T) {
	r := newTestRuntime(t)

	// Guest has no imports (so no wazero signature matching is
	// required), but manifest declares fs.reads — the FSHandle
	// must be forged regardless. Full host↔guest round-trip
	// (signature-matched imports + real host calls) is step 10.
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifestWithFSReads, wasmBytes)

	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "/tmp/test-wasm")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	if mod.Instance.FS == nil {
		t.Error("FS handle nil despite declared fs.reads")
	}
	if len(mod.Manifest.FS.Reads) != 1 {
		t.Errorf("Manifest.FS.Reads=%v, want one entry", mod.Manifest.FS.Reads)
	}
}

func TestRuntime_Instantiate_GarbageBundleRejected(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte("definitely not webassembly")
	in := signTestBundle(t, instantiateManifest, wasmBytes)

	_, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if err == nil {
		t.Fatal("want compile error on garbage bytes, got nil")
	}
}

func TestModule_Close_Idempotent(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifest, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	if err := mod.Close(context.Background()); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := mod.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestModule_Close_NilReceiver(t *testing.T) {
	var m *Module
	if err := m.Close(context.Background()); err != nil {
		t.Errorf("Close on nil module: %v", err)
	}
}

// TestRuntime_Instantiate_CascadesRevocation verifies that closing
// a Module revokes the underlying FS handle — the WASM guest cannot
// continue holding live filesystem authority after we tear down.
func TestRuntime_Instantiate_CascadesRevocation(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifestWithFSReads, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "/tmp/test-wasm")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	fs := mod.Instance.FS
	_ = mod.Close(context.Background())
	if _, err := fs.Read(context.Background(), "data/foo"); !errors.Is(err, handle.ErrRevoked) {
		t.Errorf("FS.Read after Module.Close: want ErrRevoked, got %v", err)
	}
}
