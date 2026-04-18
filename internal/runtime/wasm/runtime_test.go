package wasm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestToolHelloRoundTrip reuses the tool-hello.wasm artefact built by the
// experimental/wasm/ Makefile. If it hasn't been built yet, the test skips.
// This is deliberate: we want the integration code to live in the main
// module, but building wasip1 artefacts is a Makefile step the developer
// drives explicitly.
func TestToolHelloRoundTrip(t *testing.T) {
	manifest := repoRel(t, "experimental/wasm/examples/tool-hello/manifest.toml")
	wasmPath := repoRel(t, "experimental/wasm/examples/tool-hello/tool-hello.wasm")
	if !fileExists(wasmPath) {
		t.Skipf("skipping: %s not found — run `make build-examples` under experimental/wasm/", wasmPath)
	}

	ctx := context.Background()
	dataDir := t.TempDir()
	rt, err := New(ctx, Options{DataRoot: dataDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)

	// First invocation — counter should be 1.
	out1, _, code1, err := rt.InvokeTool(ctx, manifest, bytes.NewReader(nil), nil)
	if err != nil {
		t.Fatalf("first InvokeTool: %v", err)
	}
	if code1 != 0 {
		t.Fatalf("first exit code = %d, want 0", code1)
	}
	if !strings.Contains(string(out1), "invocation count (persisted in host-scoped storage): 1") {
		t.Errorf("first run stdout missing count=1:\n%s", out1)
	}

	// Second invocation — counter persists, expect 2.
	out2, _, code2, err := rt.InvokeTool(ctx, manifest, bytes.NewReader(nil), nil)
	if err != nil {
		t.Fatalf("second InvokeTool: %v", err)
	}
	if code2 != 0 {
		t.Fatalf("second exit code = %d, want 0", code2)
	}
	if !strings.Contains(string(out2), "invocation count (persisted in host-scoped storage): 2") {
		t.Errorf("second run stdout missing count=2:\n%s", out2)
	}
}

// TestPolicyFromManifest validates the mapping is faithful to the declared
// permissions and nothing else.
func TestPolicyFromManifest(t *testing.T) {
	m := &Manifest{
		Permissions: Permissions{
			Log:     true,
			Storage: true,
			Vault:   []string{"coingecko", "httpbin"},
			HTTP:    []string{"api.example.com"},
			Memory:  false,
			Events:  false,
		},
	}
	p := PolicyFromManifest(m)
	if !p.LogEnabled || !p.StorageEnabled {
		t.Fatalf("expected log+storage enabled, got %+v", p)
	}
	if p.MemoryEnabled || p.EventsEnabled {
		t.Fatalf("expected memory+events disabled, got %+v", p)
	}
	if !p.VaultAllowed("coingecko") || !p.VaultAllowed("httpbin") {
		t.Fatalf("coingecko / httpbin not allowed: %+v", p.VaultServices)
	}
	if p.VaultAllowed("openai") {
		t.Fatal("openai should NOT be allowed")
	}
	if !p.HTTPAllowed("api.example.com") {
		t.Fatal("api.example.com should be allowed")
	}
	if p.HTTPAllowed("evil.invalid") {
		t.Fatal("evil.invalid should NOT be allowed")
	}
}

// TestManifestValidation covers the rejection paths.
func TestManifestValidation(t *testing.T) {
	cases := map[string]Manifest{
		"missing name":    {Version: "1", Kind: KindTool, Entry: "x.wasm"},
		"missing version": {Name: "t", Kind: KindTool, Entry: "x.wasm"},
		"bad kind":        {Name: "t", Version: "1", Kind: "widget", Entry: "x.wasm"},
		"missing entry":   {Name: "t", Version: "1", Kind: KindTool},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if err := m.Validate(); err == nil {
				t.Errorf("%s: expected validation error", name)
			}
		})
	}
}

// repoRel resolves a path relative to the repository root, regardless of
// the test's package cwd. Accepts nil so benchmarks can use it too.
func repoRel(t testing.TB, rel string) string {
	if t != nil {
		t.Helper()
	}
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../alf/internal/runtime/wasm/runtime_test.go
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	return filepath.Join(root, rel)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
