package wasm

import (
	"context"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

func TestAdapter_Manifest_ProjectsEnvelopeToCapability(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifestWithFSReads, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "/tmp/test-wasm")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	a := NewAdapter(mod)
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	m := a.Manifest()
	if m.ID != capability.ID("test-wasm") {
		t.Errorf("Manifest.ID=%q, want test-wasm", m.ID)
	}
	if m.Kind != capability.KindTool {
		t.Errorf("Manifest.Kind=%d, want KindTool", m.Kind)
	}
	if m.Version != "0.1.0" {
		t.Errorf("Manifest.Version=%q, want 0.1.0", m.Version)
	}
	if m.Name != "Test WASM" {
		t.Errorf("Manifest.Name=%q", m.Name)
	}
}

const wasmAppManifest = `alf_envelope_version = 1
id      = "test-wasm-app"
kind    = "wasm-app"
version = "0.1.0"
name    = "Test WASM App"
`

func TestAdapter_Manifest_WASMAppMapsToKindApp(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, wasmAppManifest, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	a := NewAdapter(mod)
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	if a.Manifest().Kind != capability.KindApp {
		t.Errorf("Kind=%d, want KindApp", a.Manifest().Kind)
	}
}

const manifestWithReadsAndWrites = `alf_envelope_version = 1
id      = "perms-check"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Perms Check"

[[fs.reads]]
path = "data/"

[[fs.reads]]
path = "extra.txt"

[[fs.writes]]
path = "out/"
`

func TestAdapter_Permissions_UnionsReadsAndWrites(t *testing.T) {
	r := newTestRuntime(t)

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, manifestWithReadsAndWrites, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "/tmp/perms-check")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	a := NewAdapter(mod)
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	p := a.Permissions()
	if len(p.FilePaths) != 3 {
		t.Errorf("FilePaths=%v, want 3 entries (2 reads + 1 write)", p.FilePaths)
	}
}

func TestAdapter_Execute_MissingAllocExport(t *testing.T) {
	r := newTestRuntime(t)

	// Minimal guest with no exports at all.
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifest, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	a := NewAdapter(mod)
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	_, err = a.Execute(context.Background(), capability.Input{"k": "v"})
	if err == nil {
		t.Fatal("Execute with no alf_alloc export: want error, got nil")
	}
	if !strings.Contains(err.Error(), fnAlfAlloc) {
		t.Errorf("err=%v, want mention of %s", err, fnAlfAlloc)
	}
}

func TestAdapter_Execute_NilGuestRejected(t *testing.T) {
	a := &Adapter{mod: &Module{}}
	_, err := a.Execute(context.Background(), capability.Input{})
	if err == nil {
		t.Fatal("Execute with nil guest: want error, got nil")
	}
}

func TestAdapter_Close_Idempotent(t *testing.T) {
	r := newTestRuntime(t)
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	in := signTestBundle(t, instantiateManifest, wasmBytes)
	mod, err := r.Instantiate(context.Background(), in, wasmBytes, "")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	a := NewAdapter(mod)

	if err := a.Close(context.Background()); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := a.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestAdapter_Close_NilReceiver(t *testing.T) {
	var a *Adapter
	if err := a.Close(context.Background()); err != nil {
		t.Errorf("Close on nil adapter: %v", err)
	}
}

// TestAdapter_SatisfiesCapabilityInterface ensures the adapter is a
// drop-in for capability.Capability so the boot loader (step 9) can
// register it via the Registry without a shim.
func TestAdapter_SatisfiesCapabilityInterface(t *testing.T) {
	var _ capability.Capability = (*Adapter)(nil)
}

func TestEnvelopeKindMapping(t *testing.T) {
	cases := []struct {
		name string
		in   envelope.ManifestKind
		want capability.Kind
	}{
		{"wasm-tool", envelope.KindWASMTool, capability.KindTool},
		{"wasm-app", envelope.KindWASMApp, capability.KindApp},
		{"skill (unused by adapter but safe default)", envelope.KindSkill, capability.KindTool},
		{"provider (same)", envelope.KindProvider, capability.KindTool},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := envelopeKindToCapabilityKind(c.in); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
