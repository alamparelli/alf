// Package wasm is the 0.8.0 WASM runtime for ALF capabilities.
//
// Under the §3.1 ocap model, a wazero module's deny-by-default import
// table is the physical mechanism of Tier 3.1: a guest cannot call a
// host function that was not explicitly linked by the embedder. The
// forge (internal/capability/handle) decides which host functions get
// linked; this package is the one place that does the linking and the
// one place that runs guest bytecode.
//
// See docs/WASM.md (implementation spec) and
// docs/ARCHITECTURE-SECURITY.md §2.1 + §3.1 (why).
//
// Version pinning policy (WASM.md §2.1): wazero v1.11.0 is the
// current pinned version. Upgrades are reviewed quarterly for patch
// releases and require a full test-wasm pass for minor/major. CVE
// advisories against the pinned version trigger an immediate upgrade
// evaluation.
package wasm

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// Engine wraps a wazero runtime and owns every compiled module instance
// in the daemon. One Engine per daemon process — it holds compilation
// caches and wasi registration. Engine.Close() closes the underlying
// runtime and every live module; capability.handle.Instance.Close on
// each caller is the independent ocap revocation path (they race each
// other during shutdown, which is the expected design).
type Engine struct {
	rt wazero.Runtime
}

// NewEngine constructs the daemon-wide wazero runtime. Context governs
// the runtime's lifetime — cancelling it cascades to every compiled
// module and every in-flight invocation.
//
// The interpreter engine is chosen over the compiler engine for the
// initial wiring: deterministic across platforms, no JIT assumptions,
// and Layer 1 inner-ring semantics are clearer to audit. Performance
// is acceptable for the invocation rate ALF sees (§8 of WASM.md —
// ~60ms instantiate, <1ms per-call). Swap to compiler is a future
// knob once the host ABI is stable.
func NewEngine(ctx context.Context) *Engine {
	cfg := wazero.NewRuntimeConfigInterpreter().
		WithCloseOnContextDone(true)
	return &Engine{rt: wazero.NewRuntimeWithConfig(ctx, cfg)}
}

// Close shuts the wazero runtime and every module it owns. Safe to
// call multiple times — wazero's Close is idempotent. Returns any
// error reported by the runtime; callers log but do not retry.
func (e *Engine) Close(ctx context.Context) error {
	if e == nil || e.rt == nil {
		return nil
	}
	return e.rt.Close(ctx)
}

// Compile turns verified .wasm bytes into a CompiledModule ready to
// instantiate. The module is cached inside wazero keyed by the bytes
// pointer; repeated Compile of the same slice is cheap. The returned
// object is owned by the Engine — do not close it directly, let the
// Engine tear it down on Close.
//
// Error wrapping adds the package tag so the WASM layer surfaces
// distinctly in logs. The wazero-level error (which names the offending
// byte offset, section, or malformed leb128) is preserved via %w.
func (e *Engine) Compile(ctx context.Context, wasmBytes []byte) (wazero.CompiledModule, error) {
	if e == nil || e.rt == nil {
		return nil, fmt.Errorf("wasm: engine not initialised")
	}
	if len(wasmBytes) == 0 {
		return nil, fmt.Errorf("wasm: empty module bytes")
	}
	cm, err := e.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("wasm: compile: %w", err)
	}
	return cm, nil
}

// Runtime returns the underlying wazero runtime. Exposed for the rest
// of the wasm package (instantiate.go, host_fs.go) to build host
// modules and instantiate guests — never exported outside this
// package, enforced by the archtest added in step 11 (§3.5 of
// WASM.md: only this package reaches wazero).
func (e *Engine) Runtime() wazero.Runtime {
	return e.rt
}
