package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/runtime"
)

type recordingRegistry struct {
	mu   sync.Mutex
	caps []capability.ID
}

func (r *recordingRegistry) Register(c capability.Capability) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps = append(r.caps, c.Manifest().ID)
	return nil
}

// fixedNow returns a stable timestamp so Loader.Now is deterministic.
func fixedNow() time.Time {
	return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
}

// newTestLoaderRuntime builds a fresh Instantiator + Runtime + trust
// store seeded with a daemon key. Returns loader primitives ready
// for use in a single test.
func newTestLoaderRuntime(t *testing.T) (*Runtime, envelope.PrivateKey, envelope.TrustStore, *recordingRegistry) {
	t.Helper()

	handle.ResetMintForTesting()
	inst := runtime.NewInstantiator()
	rt, err := NewRuntime(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	return rt, priv, store, &recordingRegistry{}
}

const loaderManifest = `alf_envelope_version = 1
id      = "loader-test"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Loader Test"
`

// minimalWasm is a valid empty module: magic + version. Good
// enough for loader tests — instantiation succeeds, even though
// the module has no imports and no _initialize (skipped silently).
func minimalWasmBytes() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
}

func writeBundle(t *testing.T, root, id string, manifest string, wasm []byte) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoader_LoadDir_MissingRootReturnsNoErrors(t *testing.T) {
	rt, priv, store, reg := newTestLoaderRuntime(t)
	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	loaded, errs := l.LoadDir(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	if len(loaded) != 0 {
		t.Errorf("loaded=%v, want empty", loaded)
	}
	if len(errs) != 0 {
		t.Errorf("errs=%v, want none on missing root", errs)
	}
}

func TestLoader_LoadDir_AutoSignsAndRegistersUnsignedBundle(t *testing.T) {
	rt, priv, store, reg := newTestLoaderRuntime(t)
	root := t.TempDir()
	bundleDir := writeBundle(t, root, "loader-test", loaderManifest, minimalWasmBytes())

	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}

	loaded, errs := l.LoadDir(context.Background(), root)
	if len(errs) != 0 {
		t.Fatalf("errs=%v, want none", errs)
	}
	if len(loaded) != 1 || loaded[0] != "loader-test" {
		t.Errorf("loaded=%v, want [loader-test]", loaded)
	}
	if len(reg.caps) != 1 || reg.caps[0] != "loader-test" {
		t.Errorf("registry captured=%v", reg.caps)
	}

	// manifest.sig must have been persisted after auto-sign.
	sigPath := filepath.Join(bundleDir, "manifest.sig")
	if _, err := os.Stat(sigPath); err != nil {
		t.Errorf("manifest.sig missing after auto-sign: %v", err)
	}
}

// TestLoader_AutoSign_RefusesCrossFlowSubscription pins SEC-004:
// the auto-signer must reject manifests that exceed the §7.3 Tier-2
// ceiling. A manifest declaring [[events.subscribes]] is requesting
// cross-cap authority — the local daemon key cannot pre-approve it,
// so auto-sign returns ErrCeilingExceeded and the bundle is NOT
// loaded. The operator's recourse is `alf sign --key user-endorsed`.
func TestLoader_AutoSign_RefusesCrossFlowSubscription(t *testing.T) {
	rt, priv, store, reg := newTestLoaderRuntime(t)
	root := t.TempDir()
	const subManifest = `alf_envelope_version = 1
id      = "ceiling-test"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Ceiling Test"

[[events.subscribes]]
from  = "some-publisher"
topic = "evt"
`
	writeBundle(t, root, "ceiling-test", subManifest, minimalWasmBytes())

	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	loaded, errs := l.LoadDir(context.Background(), root)
	if len(loaded) != 0 {
		t.Errorf("ceiling-violating bundle loaded: %v", loaded)
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "Tier-2 ceiling") {
		t.Errorf("error should mention Tier-2 ceiling: %v", errs[0])
	}
	// manifest.sig must NOT have been written — the auto-signer
	// refused before reaching the persist step.
	sigPath := filepath.Join(root, "ceiling-test", "manifest.sig")
	if _, err := os.Stat(sigPath); err == nil {
		t.Error("manifest.sig was written despite ceiling violation")
	}
}

func TestLoader_LoadDir_ReusesExistingSignature(t *testing.T) {
	rt, priv, store, reg := newTestLoaderRuntime(t)
	root := t.TempDir()
	writeBundle(t, root, "loader-test", loaderManifest, minimalWasmBytes())

	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	// First call: auto-signs.
	if _, errs := l.LoadDir(context.Background(), root); len(errs) != 0 {
		t.Fatalf("first LoadDir: %v", errs)
	}

	// Capture the auto-signed file's mtime — and then re-instantiate
	// a fresh runtime so the registry allows re-registering.
	sigPath := filepath.Join(root, "loader-test", "manifest.sig")
	firstInfo, err := os.Stat(sigPath)
	if err != nil {
		t.Fatalf("stat sig: %v", err)
	}

	rt2, priv2, store2, reg2 := newTestLoaderRuntime(t)
	// Different priv but the existing signature was built with priv —
	// we need priv's public key in the trust store, not priv2's.
	// For this test, reuse store (which contains priv's pub).
	_ = priv2
	_ = store2

	l2 := &Loader{
		Runtime:    rt2,
		Registry:   reg2,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	if _, errs := l2.LoadDir(context.Background(), root); len(errs) != 0 {
		t.Fatalf("second LoadDir: %v", errs)
	}

	// File was not rewritten (same mtime).
	secondInfo, err := os.Stat(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		t.Errorf("manifest.sig was rewritten on reuse — first=%v, second=%v", firstInfo.ModTime(), secondInfo.ModTime())
	}
}

func TestLoader_LoadDir_NonDirectoryEntriesIgnored(t *testing.T) {
	rt, priv, store, reg := newTestLoaderRuntime(t)
	root := t.TempDir()

	// Writing a loose file next to a real bundle.
	if err := os.WriteFile(filepath.Join(root, "loose.txt"), []byte("stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeBundle(t, root, "loader-test", loaderManifest, minimalWasmBytes())

	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	loaded, errs := l.LoadDir(context.Background(), root)
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 1 {
		t.Errorf("loaded=%v, want one bundle", loaded)
	}
}

func TestLoader_LoadDir_OneBadBundleDoesNotBlockOthers(t *testing.T) {
	rt, priv, store, reg := newTestLoaderRuntime(t)
	root := t.TempDir()

	// Good bundle.
	writeBundle(t, root, "loader-test", loaderManifest, minimalWasmBytes())
	// Bad bundle — missing manifest.toml.
	if err := os.MkdirAll(filepath.Join(root, "broken"), 0o700); err != nil {
		t.Fatal(err)
	}

	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	loaded, errs := l.LoadDir(context.Background(), root)
	if len(loaded) != 1 || loaded[0] != "loader-test" {
		t.Errorf("loaded=%v", loaded)
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error for broken bundle, got %d: %v", len(errs), errs)
	}
}

func TestLoader_LoadDir_NilLoaderRejected(t *testing.T) {
	var l *Loader
	_, errs := l.LoadDir(context.Background(), "/tmp")
	if len(errs) == 0 {
		t.Fatal("want error on nil loader")
	}
}

func TestLoader_LoadDir_UnsignedBundleFailsWhenDaemonKeyNotTrusted(t *testing.T) {
	handle.ResetMintForTesting()
	inst := runtime.NewInstantiator()
	rt, err := NewRuntime(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	_, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	emptyStore := envelope.NewMemoryTrustStore() // does NOT contain the daemon key

	root := t.TempDir()
	writeBundle(t, root, "loader-test", loaderManifest, minimalWasmBytes())

	l := &Loader{
		Runtime:    rt,
		Registry:   &recordingRegistry{},
		DaemonPriv: priv,
		TrustStore: emptyStore,
		Now:        fixedNow,
	}
	loaded, errs := l.LoadDir(context.Background(), root)
	if len(loaded) != 0 {
		t.Errorf("loaded=%v, want none", loaded)
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error for untrusted daemon key, got %d", len(errs))
	}
}
