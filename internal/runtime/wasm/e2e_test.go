package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/runtime/wasm/builder"
)

// repoRoot returns the absolute path to the repo root, computed
// once from this file's own location. Tests use it to locate
// skills.d/wasm/hello-read/.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(self), "..", "..", ".."))
}

// buildHelloRead compiles skills.d/wasm/hello-read/src/ into a
// wasip1 reactor module and returns the bytes. Skips the caller
// test when the Go toolchain is not on PATH.
func buildHelloRead(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	srcDir := filepath.Join(repoRoot(t), "skills.d", "wasm", "hello-read", "src")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("read %s: %v", srcDir, err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = b
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	wasm, err := builder.Build(ctx, builder.Source{Files: files}, builder.BuildConfig{})
	if err != nil {
		t.Fatalf("builder.Build: %v", err)
	}
	return wasm
}

// helloReadManifestBytes returns the committed manifest TOML.
func helloReadManifestBytes(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(repoRoot(t), "skills.d", "wasm", "hello-read", "manifest.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// installHelloReadBundle stages the hello-read bundle in a fresh
// bundleRoot using the committed manifest + example data, and the
// freshly-built wasm bytes. Mirrors what wasm_build_tool would have
// produced, minus the signature (the loader auto-signs on discovery).
func installHelloReadBundle(t *testing.T, bundleRoot string, wasm []byte) {
	t.Helper()
	dir := filepath.Join(bundleRoot, "hello-read")
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), helloReadManifestBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello-read.wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	// Copy the committed sample data so reads have something to return.
	srcData := filepath.Join(repoRoot(t), "skills.d", "wasm", "hello-read", "data", "example.txt")
	srcF, err := os.Open(srcData)
	if err != nil {
		t.Fatal(err)
	}
	defer srcF.Close()
	dstF, err := os.Create(filepath.Join(dataDir, "example.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer dstF.Close()
	if _, err := io.Copy(dstF, srcF); err != nil {
		t.Fatal(err)
	}
}

// setupHelloReadLoader builds the sample, installs the bundle, and
// wires a Loader with a fresh daemon key + trust store. Returns the
// loader, the bundle root, and a capability registry recording the
// loaded capabilities.
func setupHelloReadLoader(t *testing.T) (*Loader, string, *capability.Registry) {
	t.Helper()
	wasm := buildHelloRead(t)

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

	bundleRoot := t.TempDir()
	installHelloReadBundle(t, bundleRoot, wasm)

	reg := capability.NewRegistry()
	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}
	return l, bundleRoot, reg
}

func TestE2E_HelloRead_HappyPath(t *testing.T) {
	l, bundleRoot, reg := setupHelloReadLoader(t)

	loaded, errs := l.LoadDir(context.Background(), bundleRoot)
	if len(errs) != 0 {
		t.Fatalf("LoadDir: %v", errs)
	}
	if len(loaded) != 1 || loaded[0] != "hello-read" {
		t.Fatalf("loaded=%v", loaded)
	}

	cap, ok := reg.Get("hello-read")
	if !ok {
		t.Fatal("hello-read not in registry")
	}

	out, err := cap.Execute(context.Background(), capability.Input{"path": "data/example.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Error != "" {
		t.Fatalf("Output.Error=%q, want empty", out.Error)
	}
	content, ok := out.Data.(string)
	if !ok {
		t.Fatalf("Output.Data type=%T, want string", out.Data)
	}
	if !strings.Contains(content, "hello from the WASM sandbox") {
		t.Errorf("Output.Data=%q, want file content", content)
	}
}

func TestE2E_HelloRead_OutOfScopePathDenied(t *testing.T) {
	l, bundleRoot, reg := setupHelloReadLoader(t)
	if _, errs := l.LoadDir(context.Background(), bundleRoot); len(errs) != 0 {
		t.Fatalf("LoadDir: %v", errs)
	}
	cap, _ := reg.Get("hello-read")

	// Path outside fs.reads = ["data/"].
	out, err := cap.Execute(context.Background(), capability.Input{"path": "../etc/passwd"})
	if err != nil {
		// Host-side error is acceptable too.
		return
	}
	if out.Error == "" {
		t.Fatalf("Execute succeeded for out-of-scope path; Data=%v", out.Data)
	}
	if !strings.Contains(out.Error, "out_of_scope") {
		t.Errorf("Output.Error=%q, want out_of_scope", out.Error)
	}
}

func TestE2E_HelloRead_MissingFileReturnsIOError(t *testing.T) {
	l, bundleRoot, reg := setupHelloReadLoader(t)
	if _, errs := l.LoadDir(context.Background(), bundleRoot); len(errs) != 0 {
		t.Fatalf("LoadDir: %v", errs)
	}
	cap, _ := reg.Get("hello-read")

	out, err := cap.Execute(context.Background(), capability.Input{"path": "data/does-not-exist.txt"})
	if err != nil {
		return
	}
	if out.Error == "" {
		t.Fatalf("Execute succeeded for missing file; Data=%v", out.Data)
	}
	if !strings.Contains(out.Error, "io_error") {
		t.Errorf("Output.Error=%q, want io_error", out.Error)
	}
}

func TestE2E_HelloRead_InvokeReturnsJSONCapabilityOutput(t *testing.T) {
	l, bundleRoot, reg := setupHelloReadLoader(t)
	if _, errs := l.LoadDir(context.Background(), bundleRoot); len(errs) != 0 {
		t.Fatalf("LoadDir: %v", errs)
	}
	cap, _ := reg.Get("hello-read")

	// Invalid input JSON is rejected by the guest and surfaces via
	// Output.Error — the adapter itself does not error.
	out, err := cap.Execute(context.Background(), capability.Input{"path": ""})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Error == "" {
		t.Error("empty-path input produced no error")
	}
	if !strings.Contains(out.Error, "path required") {
		t.Errorf("Output.Error=%q, want 'path required'", out.Error)
	}
}

// TestE2E_HelloRead_LyingManifestRejected replaces the committed
// manifest with one that drops fs.reads before loading. The guest
// still imports alf_fs_read, so CheckImports must reject.
func TestE2E_HelloRead_LyingManifestRejected(t *testing.T) {
	wasm := buildHelloRead(t)

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

	bundleRoot := t.TempDir()
	dir := filepath.Join(bundleRoot, "hello-read")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	lyingManifest := []byte(`alf_envelope_version = 1
id      = "hello-read"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Lying Hello Read"
`)
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), lyingManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello-read.wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}

	reg := capability.NewRegistry()
	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedNow,
	}

	loaded, errs := l.LoadDir(context.Background(), bundleRoot)
	if len(loaded) != 0 {
		t.Errorf("loaded=%v, want nothing registered", loaded)
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrLyingManifest) {
		t.Errorf("err=%v, want wraps ErrLyingManifest", errs[0])
	}
}

func TestE2E_HelloRead_RevocationBlocksSubsequentCalls(t *testing.T) {
	l, bundleRoot, reg := setupHelloReadLoader(t)
	if _, errs := l.LoadDir(context.Background(), bundleRoot); len(errs) != 0 {
		t.Fatalf("LoadDir: %v", errs)
	}
	cap, _ := reg.Get("hello-read")

	// First call succeeds.
	if _, err := cap.Execute(context.Background(), capability.Input{"path": "data/example.txt"}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	// Revoke the adapter's underlying module.
	adapter, ok := cap.(*Adapter)
	if !ok {
		t.Fatalf("registry capability type=%T, want *Adapter", cap)
	}
	if err := adapter.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Second call should fail — the guest was closed.
	_, err := cap.Execute(context.Background(), capability.Input{"path": "data/example.txt"})
	if err == nil {
		t.Error("Execute after Close: want error, got nil")
	}
}

// sanityCheckOutputShape asserts the guest's output is a valid
// capability.Output JSON shape. Used as a defensive check if future
// refactors change the guest ABI without updating the adapter.
func TestE2E_HelloRead_GuestOutputMatchesCapabilityOutputShape(t *testing.T) {
	l, bundleRoot, reg := setupHelloReadLoader(t)
	if _, errs := l.LoadDir(context.Background(), bundleRoot); len(errs) != 0 {
		t.Fatalf("LoadDir: %v", errs)
	}
	cap, _ := reg.Get("hello-read")

	out, err := cap.Execute(context.Background(), capability.Input{"path": "data/example.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if !strings.Contains(string(raw), `"Data"`) {
		t.Errorf("output has no Data field: %s", raw)
	}
}
