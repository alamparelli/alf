//go:build wasip1

// Package main — hello-read reference WASM tool for 0.8.0.
//
// Reads a file from the host through alf_fs_read (the only host
// import declared in manifest.toml's fs.reads scope) and returns
// its content as a capability.Output-shaped JSON body.
//
// Reactor mode (-buildmode=c-shared): main is never executed.
// _initialize runs the Go runtime once at instantiation; the
// host then calls the wasmexport functions on each invocation.
package main

import (
	"encoding/json"
	"unsafe"
)

//go:wasmimport alf alf_fs_read
func alfFsRead(pathPtr, pathLen, outPtr, outMax uint32) uint64

// allocated keeps every buffer we hand to the host alive across GC.
// Without this, Go's WASM GC would reclaim a slice as soon as the
// function that created it returns, invalidating the pointer the
// host is about to read or write.
var allocated = map[uint32][]byte{}

// outBuf holds the last JSON response. It lives in a package-level
// slot so the packed pointer returned by alf_invoke remains valid
// until the host completes the Memory.Read and the next call
// overwrites it.
var outBuf []byte

func main() {}

//go:wasmexport alf_alloc
func alfAlloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	allocated[ptr] = buf
	return ptr
}

//go:wasmexport alf_invoke
func alfInvoke(inPtr, inLen uint32) uint64 {
	inBuf, ok := allocated[inPtr]
	if !ok || uint32(len(inBuf)) < inLen {
		return respondError("input buffer not tracked or short")
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(inBuf[:inLen], &req); err != nil {
		return respondError("invalid input json: " + err.Error())
	}
	if req.Path == "" {
		return respondError("path required")
	}

	const maxRead = 64 * 1024
	scratch := make([]byte, maxRead)
	pathBytes := []byte(req.Path)
	allocated[ptrOf(pathBytes)] = pathBytes
	allocated[ptrOf(scratch)] = scratch

	r := alfFsRead(
		ptrOf(pathBytes), uint32(len(pathBytes)),
		ptrOf(scratch), uint32(len(scratch)),
	)
	errCode := uint32(r >> 32)
	outLen := uint32(r & 0xFFFFFFFF)
	if errCode != 0 {
		return respondError(errName(errCode))
	}
	return respondData(string(scratch[:outLen]))
}

// output mirrors capability.Output so host-side json.Unmarshal
// round-trips cleanly. Data is string-typed here because hello-read
// only returns file content; richer capabilities would use `any`.
type output struct {
	Data  string `json:"Data"`
	Error string `json:"Error"`
}

func respondData(s string) uint64 {
	outBuf, _ = json.Marshal(output{Data: s})
	return pack(outBuf)
}

func respondError(msg string) uint64 {
	outBuf, _ = json.Marshal(output{Error: msg})
	return pack(outBuf)
}

func pack(b []byte) uint64 {
	ptr := ptrOf(b)
	return (uint64(ptr) << 32) | uint64(len(b))
}

func ptrOf(b []byte) uint32 {
	if len(b) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

// errName maps the WASM.md §3.3 host-ABI error codes to human
// strings for the JSON response.
func errName(code uint32) string {
	switch code {
	case 1:
		return "revoked"
	case 2:
		return "out_of_scope"
	case 3:
		return "io_error"
	case 4:
		return "buffer_too_small"
	default:
		return "unknown"
	}
}
