//go:build wasip1

// Package main — http-hello smoke-test WASM tool for #421 Wave 2.
//
// Reads input JSON {"url": "https://httpbin.org/get"} from the host
// (via alf_invoke), issues a GET request through alf_http_request,
// and returns {"status": int, "body": string} or {"error": string}.
//
// Reactor mode (-buildmode=c-shared): main is never executed.
// _initialize runs the Go runtime once at instantiation; the host
// then calls the wasmexport functions on each invocation.
package main

import (
	"encoding/json"
	"unsafe"
)

//go:wasmimport alf alf_http_request
func alfHTTPRequest(
	methodPtr, methodLen uint32,
	urlPtr, urlLen uint32,
	headersPtr, headersLen uint32,
	bodyPtr, bodyLen uint32,
	outStatusPtr uint32,
	outBodyPtr, outBodyMax uint32,
) uint64

// allocated keeps every buffer we hand to the host alive across GC.
// Without this, Go's WASM GC would reclaim a slice as soon as the
// function that created it returns, invalidating the pointer the
// host is about to read or write.
var allocated = map[uint32][]byte{}

// outBuf holds the last JSON response. Lives in a package-level slot
// so the packed pointer returned by alf_invoke remains valid until
// the host completes Memory.Read.
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
		URL string `json:"url"`
	}
	if err := json.Unmarshal(inBuf[:inLen], &req); err != nil {
		return respondError("invalid input json: " + err.Error())
	}
	if req.URL == "" {
		return respondError("url required")
	}

	const maxBody = 256 * 1024
	urlBytes := []byte(req.URL)
	methodBytes := []byte("GET")
	respBody := make([]byte, maxBody)
	var status uint32

	allocated[ptrOf(urlBytes)] = urlBytes
	allocated[ptrOf(methodBytes)] = methodBytes
	allocated[ptrOf(respBody)] = respBody

	// Pin the address of `status` so the host can write to it.
	statusPtr := uint32(uintptr(unsafe.Pointer(&status)))

	r := alfHTTPRequest(
		ptrOf(methodBytes), uint32(len(methodBytes)),
		ptrOf(urlBytes), uint32(len(urlBytes)),
		0, 0, // no headers
		0, 0, // no body
		statusPtr,
		ptrOf(respBody), uint32(len(respBody)),
	)
	errCode := uint32(r >> 32)
	outLen := uint32(r & 0xFFFFFFFF)
	if errCode != 0 && errCode != 5 { // 5 = http_status_non_2xx (we still get a body)
		return respondError(errName(errCode))
	}
	return respondOK(int(status), string(respBody[:outLen]))
}

// output mirrors capability.Output so host-side json.Unmarshal
// round-trips cleanly.
type output struct {
	Status int    `json:"status,omitempty"`
	Body   string `json:"body,omitempty"`
	Error  string `json:"error,omitempty"`
}

func respondOK(status int, body string) uint64 {
	outBuf, _ = json.Marshal(output{Status: status, Body: body})
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

// errName maps the host-ABI error codes to human strings.
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
	case 5:
		return "http_status_non_2xx"
	case 6:
		return "tls_failure"
	default:
		return "unknown"
	}
}
