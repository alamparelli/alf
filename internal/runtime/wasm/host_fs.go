package wasm

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// Host ABI error codes (WASM.md §3.3). Structural: they flow from the
// handle layer, a host function cannot invent a new class.
const (
	errOK              uint32 = 0
	errRevoked         uint32 = 1
	errOutOfScope      uint32 = 2
	errIO              uint32 = 3
	errBufferTooSmall  uint32 = 4
)

// packResult encodes the (err, out_len) pair into a single i64 per the
// WASM.md §3.2 host ABI. Go's //go:wasmimport does not cleanly support
// multi-return across target toolchains, so the host returns one i64
// and guest unpacks with (r >> 32), (r & 0xFFFFFFFF).
func packResult(err, outLen uint32) uint64 {
	return (uint64(err) << 32) | uint64(outLen)
}

// BuildHostModule registers the shared "alf" host module on rt. Because
// wazero rejects duplicate module names, this MUST be called exactly
// once per runtime — the daemon does so at NewRuntime time, after which
// every guest instantiated on the same runtime imports from the same
// shared module. Per-guest authority is routed via reg.Lookup(mod.Name()):
// each host function reads the calling guest's wazero module name and
// fetches its FSHandle from the registry the loader populated at forge.
//
// Always-export model: alf_fs_read and alf_fs_write are unconditionally
// exported. The structural gate against ambient access is `CheckImports`
// (run before InstantiateModule), which fails the guest if it imports a
// function its manifest did not declare. The previous "conditional
// export" pattern was belt-and-braces; CheckImports remains the braces.
//
// Returns the instantiated host module — the runtime owns its lifetime
// (closed when the engine closes).
func BuildHostModule(ctx context.Context, rt wazero.Runtime, reg *hostFSRegistry) (api.Module, error) {
	if reg == nil {
		return nil, fmt.Errorf("wasm: BuildHostModule requires a host registry")
	}
	b := rt.NewHostModuleBuilder(hostModuleALF)
	b.NewFunctionBuilder().
		WithFunc(makeFSReadDispatch(reg)).
		WithParameterNames("path_ptr", "path_len", "out_ptr", "out_max").
		Export(fnAlfFSRead)
	b.NewFunctionBuilder().
		WithFunc(makeFSWriteDispatch(reg)).
		WithParameterNames("path_ptr", "path_len", "data_ptr", "data_len").
		Export(fnAlfFSWrite)

	mod, err := b.Instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("wasm: build host module: %w", err)
	}
	return mod, nil
}

// makeFSReadDispatch returns the alf_fs_read implementation. It looks
// up the calling guest's FSHandle via mod.Name() — if no handle is
// registered (guest instantiated outside the forge path, or registered
// with empty scope), the call returns errIO. Same memory-only access
// pattern as the original makeFSRead — archtest TestWASMHostFSUsesMemoryReadWriteOnly
// continues to enforce it.
func makeFSReadDispatch(reg *hostFSRegistry) func(context.Context, api.Module, uint32, uint32, uint32, uint32) uint64 {
	return func(ctx context.Context, mod api.Module, pathPtr, pathLen, outPtr, outMax uint32) uint64 {
		fs := reg.Lookup(mod.Name())
		if fs == nil {
			return packResult(errIO, 0)
		}
		return makeFSRead(fs)(ctx, mod, pathPtr, pathLen, outPtr, outMax)
	}
}

// makeFSWriteDispatch is the alf_fs_write counterpart. Returns uint32
// (err_code only) per WASM.md §3.2.
func makeFSWriteDispatch(reg *hostFSRegistry) func(context.Context, api.Module, uint32, uint32, uint32, uint32) uint32 {
	return func(ctx context.Context, mod api.Module, pathPtr, pathLen, dataPtr, dataLen uint32) uint32 {
		fs := reg.Lookup(mod.Name())
		if fs == nil {
			return errIO
		}
		return makeFSWrite(fs)(ctx, mod, pathPtr, pathLen, dataPtr, dataLen)
	}
}

// makeFSRead closes over the per-instance FSHandle and returns the
// host-function implementation wazero will reflect-invoke. The guest
// passes (path_ptr, path_len, out_ptr, out_max); the host reads the
// path from guest memory (bounds-checked), calls FSHandle.Read (which
// enforces scope + revocation), then writes the result into guest
// memory. All memory access uses api.Memory.Read / Write — never raw
// pointer arithmetic from guest-supplied offsets (archtest step 11
// enforces this).
func makeFSRead(fs *handle.FSHandle) func(context.Context, api.Module, uint32, uint32, uint32, uint32) uint64 {
	return func(ctx context.Context, mod api.Module, pathPtr, pathLen, outPtr, outMax uint32) uint64 {
		mem := mod.Memory()
		pathBytes, ok := mem.Read(pathPtr, pathLen)
		if !ok {
			return packResult(errIO, 0)
		}
		data, err := fs.Read(ctx, string(pathBytes))
		if err != nil {
			return packResult(classifyFSError(err), 0)
		}
		if uint32(len(data)) > outMax {
			// Guest buffer too small — report the required size in
			// out_len so the guest can realloc and retry.
			return packResult(errBufferTooSmall, uint32(len(data)))
		}
		if !mem.Write(outPtr, data) {
			return packResult(errIO, 0)
		}
		return packResult(errOK, uint32(len(data)))
	}
}

// makeFSWrite closes over the per-instance FSHandle and returns the
// host-function implementation for alf_fs_write. Returns err_code
// only (no out_len — write has no return payload). Signature is
// uint32 rather than uint64 per WASM.md §3.2.
func makeFSWrite(fs *handle.FSHandle) func(context.Context, api.Module, uint32, uint32, uint32, uint32) uint32 {
	return func(ctx context.Context, mod api.Module, pathPtr, pathLen, dataPtr, dataLen uint32) uint32 {
		mem := mod.Memory()
		pathBytes, ok := mem.Read(pathPtr, pathLen)
		if !ok {
			return errIO
		}
		data, ok := mem.Read(dataPtr, dataLen)
		if !ok {
			return errIO
		}
		// Copy data out of the guest memory window — FSHandle.Write
		// may block, and the guest could theoretically resize memory
		// while we wait, invalidating the shared slice. api.Memory
		// documents Read as returning a slice into live guest memory,
		// so we must materialise bytes we intend to hold across the
		// syscall.
		cp := make([]byte, len(data))
		copy(cp, data)

		if err := fs.Write(ctx, string(pathBytes), cp); err != nil {
			return classifyFSError(err)
		}
		return errOK
	}
}

// classifyFSError maps handle-layer errors to the structural host-ABI
// error codes per WASM.md §3.3. Everything unrecognised maps to errIO
// — the guest treats unknowns as transient filesystem failures.
func classifyFSError(err error) uint32 {
	switch err {
	case handle.ErrRevoked:
		return errRevoked
	case handle.ErrOutOfScope:
		return errOutOfScope
	default:
		return errIO
	}
}
