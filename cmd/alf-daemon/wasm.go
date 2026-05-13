package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
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
	//
	// TrustStore is a DirTrustStore so operator-managed keys persist
	// across restarts under <dataDir>/trust/. The CLI side mutates the
	// directory directly via DirTrustStore.Persist / PersistRemove /
	// PersistRevoke; the running daemon picks up changes on the next
	// SIGHUP-driven Load() (#395 Stage 2 follow-up) or on restart.
	Inst       *runtime.Instantiator
	DaemonPriv envelope.PrivateKey
	TrustStore *envelope.DirTrustStore

	// HandleRegistry carries the live set of registered handle kinds
	// (#392 Stage 2). At boot it holds the alf: namespace seeded from
	// AlfCoreHandleIDs; Stage 3 will populate it with installed
	// providers' [[provider.exports]] under their fingerprint short.
	// Lookup is concurrent-safe; mutation goes through the Instantiator
	// (which holds the §4.3 runtime token) via SeedHandleRegistry.
	HandleRegistry *handle.HandleRegistry
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
// The runtime ships unconditionally as of v0.8.0: the dev-window
// ALF_EXPERIMENTAL gate was retired with the strict-flip. WASM bundles
// load + verify + register at boot whenever <skillsDir>/wasm exists.
func setupWASMLoader(ctx context.Context, dataDir, skillsDir string, registry wasm.CapabilityRegistry, logf func(string, ...any)) (*wasmRuntime, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	pub, priv, err := wasm.LoadOrGenerateDaemonKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("wasm-loader: daemon key: %w", err)
	}

	// Trust store is dir-backed under <dataDir>/trust/ so admin CLI
	// mutations (alf trust add/remove/revoke) survive restarts. The
	// daemon key itself is NOT persisted here — it lives in keys/
	// alongside its private half (auto-bootstrap, never operator-
	// editable). On boot we Add() it to the in-memory side so the
	// verify path admits daemon-signed bundles without writing a
	// corresponding .pub file an operator might think is theirs.
	store := envelope.NewDirTrustStore(filepath.Join(dataDir, "trust"))
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("wasm-loader: trust store load: %w", err)
	}
	store.Add(pub)
	logf("[wasm-loader] trust store: dir=%s operator-keys=%d", store.Dir(), len(store.Keys())-1)

	// Events plumbing (#399): the bus is the in-memory router, the
	// cross-flow registry is populated by the loader's pass 1 from
	// every manifest's events.exports. Both wired into the Instantiator
	// so InstantiateVerified forges EventPub/EventSub handles when a
	// manifest declares events blocks.
	bus := events.New()
	crossFlow := events.NewMemoryRegistry()

	// #392 Stage 2/3 — runtime handle registry. Seeded with the daemon's
	// bundled core kinds under the alf: namespace; capability-provider
	// bundles add their [[provider.exports]] entries under the publisher's
	// fingerprint short via Instantiator.RegisterProviderExports during
	// InstantiateVerified. The registry is wired via WithHandleRegistry
	// so InstantiateVerified can also resolve [[depends]] against it.
	// SeedHandleRegistry stays as the explicit boot-seed call — the
	// option only stores the registry; seeding is a separate step so a
	// duplicate-seed wiring bug surfaces loudly rather than racing
	// during option processing.
	handleRegistry := handle.NewHandleRegistry()
	inst := runtime.NewInstantiator(
		runtime.WithEventsBus(bus, bus),
		runtime.WithCrossFlowRegistry(crossFlow),
		runtime.WithHandleRegistry(handleRegistry),
		// #421 Wave 2: WASM-app HTTP egress. The default client uses
		// http.DefaultTransport, which honours HTTP_PROXY / HTTPS_PROXY
		// env vars; the daemon sets these at boot (main.go) to point at
		// the firewall proxy on 127.0.0.1:4751 — so every WASM-originated
		// request transparently crosses the operator's domain allow/deny
		// rules and lands in the firewall request log alongside everything
		// else the daemon emits.
		runtime.WithHTTPClient(wasm.NewDefaultHTTPClient()),
	)
	if err := inst.SeedHandleRegistry(handleRegistry); err != nil {
		return nil, fmt.Errorf("wasm-loader: seed handle registry: %w", err)
	}
	logf("[wasm-loader] handle registry seeded: %d core kinds (alf:*)", handleRegistry.Len())

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

	// #420 — under §4.1 the loader scans <dataDir>/tools/<id>/ for
	// wasm-tool bundles and <dataDir>/apps/<slug>/ for wasm-app bundles.
	// Any bundle still in the legacy <dataDir>/skills.d/wasm/<id>/
	// layout is migrated to the new path based on its manifest kind
	// before the scan runs (LoadAll handles this).
	_ = skillsDir // legacy path kept in signature for #392 future use
	loaded, errs := loader.LoadAll(ctx, dataDir)
	logf("[wasm-loader] scanned %s/{tools,apps}: %d bundles loaded, %d errors", dataDir, len(loaded), len(errs))
	for _, e := range errs {
		logf("[wasm-loader] error: %v", e)
	}

	return &wasmRuntime{
		rt:             rt,
		loader:         loader,
		bus:            bus,
		crossFlow:      crossFlow,
		Inst:           inst,
		DaemonPriv:     priv,
		TrustStore:     store,
		HandleRegistry: handleRegistry,
	}, nil
}
