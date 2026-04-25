package wasm

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/runtime"
)

// Module is the runtime-ready result of Runtime.Instantiate: a
// verified, import-checked, forge-grant-backed, reactor-initialised
// guest ready to be invoked by the capability adapter (step 6).
//
// The fields are exported so the adapter and the boot-time loader
// can reach Instance (for ocap revocation), Manifest (for tool-schema
// generation), and Guest (for Invoke). Compiled and Host are kept for
// lifecycle Close — callers don't touch them.
type Module struct {
	Instance *handle.Instance
	Manifest *envelope.Manifest
	Guest    api.Module

	compiled       wazero.CompiledModule
	hostUnregister func() // unregisters this guest's FSHandle from the runtime's host registry
}

// Close tears down the module: first cancels the handle.Instance
// (which cascades ocap revocation — subsequent host calls return
// ErrRevoked), then closes the wazero guest, host module, and
// compiled module. Safe to call multiple times — each inner Close
// is idempotent at the wazero layer.
//
// Callers that want to drop the module early (policy reload,
// bundle update) invoke this; normal shutdown runs the Runtime's
// owning Engine.Close which cascades via WithCloseOnContextDone.
func (m *Module) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.Instance != nil {
		m.Instance.Close()
	}
	var first error
	set := func(err error) {
		if first == nil && err != nil {
			first = err
		}
	}
	if m.Guest != nil {
		set(m.Guest.Close(ctx))
	}
	if m.hostUnregister != nil {
		m.hostUnregister()
	}
	if m.compiled != nil {
		set(m.compiled.Close(ctx))
	}
	return first
}

// Runtime composes runtime.Instantiator (envelope.Verify + ocap
// forge) with a wasm.Engine (wazero compile + instantiate) to
// produce live guest modules.
//
// One Runtime per daemon process. It owns the WASI import
// registration, the shared "alf" host module, the per-guest FS
// handle registry, and the wasm.Engine lifecycle. NewRuntime must be
// called after the Instantiator has been constructed (the runtime
// token is minted there).
type Runtime struct {
	engine  *Engine
	inst    *runtime.Instantiator
	hostMod api.Module       // the singleton "alf" host module
	hostReg *hostFSRegistry  // per-guest FSHandle dispatch table
}

// NewRuntime constructs the daemon-wide WASM runtime. It takes over
// the provided ctx for the wazero runtime's lifetime (cancelling the
// ctx cascades to every guest instance via WithCloseOnContextDone).
// WASI preview 1 is registered once at construction; the shared "alf"
// host module is registered next so subsequent Instantiate calls only
// need to register the per-guest FSHandle in the dispatch table.
func NewRuntime(ctx context.Context, inst *runtime.Instantiator) (*Runtime, error) {
	if inst == nil {
		return nil, fmt.Errorf("wasm: NewRuntime requires a non-nil Instantiator")
	}
	e := NewEngine(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, e.Runtime()); err != nil {
		_ = e.Close(ctx)
		return nil, fmt.Errorf("wasm: register WASI preview 1: %w", err)
	}
	reg := newHostFSRegistry()
	hostMod, err := BuildHostModule(ctx, e.Runtime(), reg)
	if err != nil {
		_ = e.Close(ctx)
		return nil, fmt.Errorf("wasm: register host module: %w", err)
	}
	return &Runtime{engine: e, inst: inst, hostMod: hostMod, hostReg: reg}, nil
}

// Close tears down the WASM runtime. All live modules are closed
// by wazero as a consequence.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return r.engine.Close(ctx)
}

// Instantiate is the single production entry point to go from an
// on-disk bundle (in-memory bytes) to a running guest module. It
// composes the five load-time invariants in a single call, in the
// order fixed by ARCHITECTURE-SECURITY §7.1 / WASM.md §7.1:
//
//  1. envelope.Verify via Instantiator.InstantiateVerified —
//     signature + trust store + schema + canonicalisation + bundle
//     hash cross-check. Produces the forged handle.Instance and the
//     typed Manifest.
//  2. Engine.Compile — wazero parses the guest bytes.
//  3. CheckImports — guest imports ⊆ manifest declarations + allowed
//     modules. Lying manifests die here, before any code runs.
//  4. BuildHostModule — the "alf" host module, with alf_fs_* exported
//     iff the forged FSHandle has non-empty scope.
//  5. wazero.InstantiateModule with WithStartFunctions("_initialize")
//     — reactor mode per WASM.md §4.2: _initialize runs Go runtime
//     init and returns; main is never executed.
//
// wasmBytes MUST be the same slice that in.Bundle carries — envelope
// hashed those bytes during step 1, and handing a different slice to
// wazero in step 2 would break TOCTOU safety. Caller discipline
// (step 9 boot loader, step 8 wasm_build_tool) satisfies this.
//
// On any error, all resources acquired up to that point are cleaned
// up before return.
func (r *Runtime) Instantiate(ctx context.Context, in envelope.VerifyInput, wasmBytes []byte, baseDir string) (*Module, error) {
	if r == nil || r.engine == nil || r.inst == nil {
		return nil, fmt.Errorf("wasm: runtime not initialised")
	}
	if len(wasmBytes) == 0 {
		return nil, fmt.Errorf("wasm: empty wasm bytes")
	}

	// 1. Verify + forge.
	vi, err := r.inst.InstantiateVerified(ctx, in, baseDir)
	if err != nil {
		return nil, err
	}
	cleanupInst := func() { vi.Instance.Close() }

	// 2. Compile.
	cm, err := r.engine.Compile(ctx, wasmBytes)
	if err != nil {
		cleanupInst()
		return nil, err
	}
	cleanupCM := func() { _ = cm.Close(ctx) }

	// 3. Cross-check imports. Runs on the compiled module — wazero
	// has parsed the binary, so we know the import list is
	// authoritative (no truncation, no malformed leb128 that a
	// naive parser might misread).
	if err := CheckImports(cm, vi.Manifest); err != nil {
		cleanupCM()
		cleanupInst()
		return nil, err
	}

	// 4. Register this guest's FSHandle in the runtime's per-guest
	// dispatch table. The shared "alf" host module (registered at
	// NewRuntime) routes alf_fs_* calls to this handle by reading
	// the calling guest's wazero module name.
	guestName := string(vi.Manifest.ID)
	r.hostReg.Register(guestName, vi.Instance.FS)
	cleanupHostReg := func() { r.hostReg.Unregister(guestName) }

	// 5. Instantiate guest in reactor mode. WithStartFunctions
	// replaces the default "_start" with "_initialize" — if the
	// module has _initialize, wazero calls it and returns; otherwise
	// it's silently skipped (wazero v1.11 config.go).
	modCfg := wazero.NewModuleConfig().
		WithName(guestName).
		WithStartFunctions("_initialize")
	guest, err := r.engine.Runtime().InstantiateModule(ctx, cm, modCfg)
	if err != nil {
		cleanupHostReg()
		cleanupCM()
		cleanupInst()
		return nil, fmt.Errorf("wasm: instantiate guest: %w", err)
	}

	return &Module{
		Instance:       vi.Instance,
		Manifest:       vi.Manifest,
		Guest:          guest,
		compiled:       cm,
		hostUnregister: cleanupHostReg,
	}, nil
}
