package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/runtime/events"
	"github.com/alamparelli/alf/internal/runtime/wasm"
)

// wasmLoaderRoot is the bundle directory layout the daemon scans on
// boot: <skillsDir>/wasm/<id>/{manifest.toml, <id>.wasm, manifest.sig?}.
// Mirrors the layout fixed by Loader's docs and the hello-read fixture
// shipped in #386 step 10.
const wasmLoaderRoot = "wasm"

// wasmRuntime bundles the daemon-owned WASM lifecycle so main() has a
// single object to defer-close on shutdown. The bus + cross-flow
// registry are kept on the struct so future hot-reload paths (#395
// follow-up) can reuse them across LoadDir invocations.
type wasmRuntime struct {
	rt        *wasm.Runtime
	loader    *wasm.Loader
	bus       *events.Bus
	crossFlow *events.MemoryRegistry

	// Inst is the shared Instantiator. A single RuntimeToken is minted
	// per process (§4.3 invariant), so any other loader that needs to
	// forge handles (skill loader, future provider loader) must reuse
	// this one. DaemonPriv + TrustStore are exposed for the same reason
	// — every kind shares the §7.3 Tier 2 daemon key.
	Inst       *runtime.Instantiator
	DaemonPriv envelope.PrivateKey
	TrustStore *envelope.MemoryTrustStore
}

// Close tears down the wazero runtime. Safe on nil receiver so main()
// can defer unconditionally.
func (w *wasmRuntime) Close(ctx context.Context) error {
	if w == nil || w.rt == nil {
		return nil
	}
	return w.rt.Close(ctx)
}

// setupWASMLoader wires the §7.1 verify+forge+instantiate pipeline at
// boot: daemon key (auto-generated on first boot), trust store seeded
// with that key, runtime.Instantiator, wasm.Runtime, wasm.Loader. Then
// it scans <skillsDir>/wasm and registers every bundle that verifies.
//
// Per-bundle failures are logged and accumulated — they never abort
// the boot sequence (see Loader.LoadDir doc). The returned error is
// reserved for stack-init failures (key generation, runtime construction)
// that mean nothing else can succeed.
//
// The runtime is gated by the existing ALF_EXPERIMENTAL=1 boot check
// (see experimental.go); no separate flag — running the daemon during
// the 0.8.0 dev window already implies opt-in.
func setupWASMLoader(ctx context.Context, dataDir, skillsDir string, registry wasm.CapabilityRegistry, logf func(string, ...any)) (*wasmRuntime, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	pub, priv, err := wasm.LoadOrGenerateDaemonKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("wasm-loader: daemon key: %w", err)
	}

	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	// Events plumbing (#399): the bus is the in-memory router, the
	// cross-flow registry is populated by the loader's pass 1 from
	// every manifest's events.exports. Both wired into the Instantiator
	// so InstantiateVerified forges EventPub/EventSub handles when a
	// manifest declares events blocks.
	bus := events.New()
	crossFlow := events.NewMemoryRegistry()

	inst := runtime.NewInstantiator(
		runtime.WithEventsBus(bus, bus),
		runtime.WithCrossFlowRegistry(crossFlow),
	)
	rt, err := wasm.NewRuntime(ctx, inst)
	if err != nil {
		return nil, fmt.Errorf("wasm-loader: new runtime: %w", err)
	}

	loader := &wasm.Loader{
		Runtime:     rt,
		Registry:    registry,
		DaemonPriv:  priv,
		TrustStore:  store,
		Logger:      logf,
		CrossFlow:   crossFlow,
		SnapshotDir: dataDir,
	}

	root := filepath.Join(skillsDir, wasmLoaderRoot)
	loaded, errs := loader.LoadDir(ctx, root)
	logf("[wasm-loader] scanned %s: %d bundles loaded, %d errors", root, len(loaded), len(errs))
	for _, e := range errs {
		logf("[wasm-loader] error: %v", e)
	}

	return &wasmRuntime{
		rt:         rt,
		loader:     loader,
		bus:        bus,
		crossFlow:  crossFlow,
		Inst:       inst,
		DaemonPriv: priv,
		TrustStore: store,
	}, nil
}
