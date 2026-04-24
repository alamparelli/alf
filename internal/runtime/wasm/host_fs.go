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

// BuildHostModule compiles the "alf" host module for a single guest
// instance. The FS handle is captured in closure — each guest gets
// its own host module bound to its own forged handle. Functions are
// exported CONDITIONALLY on scope: a guest whose manifest declared no
// fs.reads receives a module without alf_fs_read, and wazero will
// refuse to instantiate a guest that imports a function the host
// module does not export. This is the "§3.5 only linked if authorised"
// belt in WASM.md; CheckImports is the braces (step 3).
//
// Returns the instantiated host module and any error from wazero.
// The module's lifetime is tied to the runtime; it is closed implicitly
// when the runtime closes, so callers do not need to track it
// separately from the guest module it serves.
//
// If fs is nil (manifest declared no fs at all), the host module is
// empty — no alf_fs_* functions. A guest that imports them will fail
// to instantiate, which is the desired structural outcome.
func BuildHostModule(ctx context.Context, rt wazero.Runtime, fs *handle.FSHandle) (api.Module, error) {
	b := rt.NewHostModuleBuilder(hostModuleALF)

	if fs != nil && len(fs.Scope().Reads) > 0 {
		b.NewFunctionBuilder().
			WithFunc(makeFSRead(fs)).
			WithParameterNames("path_ptr", "path_len", "out_ptr", "out_max").
			Export(fnAlfFSRead)
	}
	if fs != nil && len(fs.Scope().Writes) > 0 {
		b.NewFunctionBuilder().
			WithFunc(makeFSWrite(fs)).
			WithParameterNames("path_ptr", "path_len", "data_ptr", "data_len").
			Export(fnAlfFSWrite)
	}

	mod, err := b.Instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("wasm: build host module: %w", err)
	}
	return mod, nil
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
