package wasm

import (
	"bytes"
	"context"
	"testing"
)

// BenchmarkToolInvocation measures the warm-invocation cost after the
// compile cache kicks in. Compare b.N > 1 runs against the spike's
// uncached ~700 ms to verify the Runtime's compile cache works.
//
// Skip if the wasm artefact is not present (see TestToolHelloRoundTrip).
func BenchmarkToolInvocation(b *testing.B) {
	manifest := repoRel(nil, "experimental/wasm/examples/tool-hello/manifest.toml")
	wasmPath := repoRel(nil, "experimental/wasm/examples/tool-hello/tool-hello.wasm")
	if !fileExists(wasmPath) {
		b.Skipf("skipping: %s not built — run `make build-examples` under experimental/wasm/", wasmPath)
	}

	ctx := context.Background()
	rt, err := New(ctx, Options{DataRoot: b.TempDir()})
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer rt.Close(ctx)

	// Prime the compile cache with one invocation outside the timer.
	if _, _, _, err := rt.InvokeTool(ctx, manifest, bytes.NewReader(nil), nil); err != nil {
		b.Fatalf("prime: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := rt.InvokeTool(ctx, manifest, bytes.NewReader(nil), nil); err != nil {
			b.Fatalf("invoke: %v", err)
		}
	}
}
