// Package alf is the guest SDK: helpers that wrap host imports exposed by the
// alf-wasm-host runtime. A capability (tool or app) imports this package and
// calls Log, Storage, Vault, etc. Each call is a wasmimport — the host decides
// whether it is allowed based on the manifest; if the capability did not
// declare the permission, the import is absent and the module fails to link
// at instantiation (this is the sandbox's structural guarantee).
//
// This file only compiles under `GOOS=wasip1 GOARCH=wasm`. Host code does
// not import it.
//
//go:build wasip1

package alf

import (
	"encoding/json"
	"os"
	"strconv"
	"unsafe"
)

// Maximum input/output buffer for variable-size host calls. A real SDK would
// allocate dynamically; the spike keeps it simple with a 128 KB static buffer.
const maxIOSize = 128 * 1024

var ioBuf [maxIOSize]byte

// -----------------------------------------------------------------------------
// Log
// -----------------------------------------------------------------------------

//go:wasmimport alf log_info
func hostLogInfo(ptr *byte, length uint32)

//go:wasmimport alf log_error
func hostLogError(ptr *byte, length uint32)

// LogInfo writes msg to the host's log (informational).
func LogInfo(msg string) {
	if len(msg) == 0 {
		return
	}
	hostLogInfo(unsafe.StringData(msg), uint32(len(msg)))
}

// LogError writes msg to the host's log at error level.
func LogError(msg string) {
	if len(msg) == 0 {
		return
	}
	hostLogError(unsafe.StringData(msg), uint32(len(msg)))
}

// -----------------------------------------------------------------------------
// Storage
// -----------------------------------------------------------------------------

//go:wasmimport alf storage_put
func hostStoragePut(keyPtr *byte, keyLen uint32, valPtr *byte, valLen uint32) int32

//go:wasmimport alf storage_get_len
func hostStorageGetLen(keyPtr *byte, keyLen uint32) int32

//go:wasmimport alf storage_get
func hostStorageGet(keyPtr *byte, keyLen uint32, outPtr *byte, outCap uint32) int32

//go:wasmimport alf storage_delete
func hostStorageDelete(keyPtr *byte, keyLen uint32) int32

// StoragePut stores value under key. Returns an error if the host denies or
// fails. If the capability did not declare `storage = true`, this call will
// not link.
func StoragePut(key string, value []byte) error {
	var vp *byte
	if len(value) > 0 {
		vp = &value[0]
	} else {
		vp = &ioBuf[0] // unused but non-nil
	}
	rc := hostStoragePut(
		unsafe.StringData(key), uint32(len(key)),
		vp, uint32(len(value)),
	)
	if rc < 0 {
		return hostError("storage.put", rc)
	}
	return nil
}

// StorageGet returns the value for key and ok=true if present.
func StorageGet(key string) ([]byte, bool) {
	n := hostStorageGetLen(unsafe.StringData(key), uint32(len(key)))
	if n < 0 {
		return nil, false
	}
	if int(n) > len(ioBuf) {
		// Value exceeds the static buffer. A production SDK would grow
		// dynamically; for the spike we fail loudly rather than silently.
		LogError("storage.get: value larger than SDK static buffer (" + strconv.Itoa(int(n)) + " bytes)")
		return nil, false
	}
	got := hostStorageGet(
		unsafe.StringData(key), uint32(len(key)),
		&ioBuf[0], uint32(len(ioBuf)),
	)
	if got < 0 {
		return nil, false
	}
	out := make([]byte, got)
	copy(out, ioBuf[:got])
	return out, true
}

// StorageDelete removes key. Returns nil if absent.
func StorageDelete(key string) error {
	rc := hostStorageDelete(unsafe.StringData(key), uint32(len(key)))
	if rc < 0 {
		return hostError("storage.delete", rc)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Vault (per-service allowlist)
// -----------------------------------------------------------------------------

//go:wasmimport alf vault_request_len
func hostVaultRequestLen(svcPtr *byte, svcLen uint32, pathPtr *byte, pathLen uint32) int32

//go:wasmimport alf vault_request
func hostVaultRequest(svcPtr *byte, svcLen uint32, pathPtr *byte, pathLen uint32, outPtr *byte, outCap uint32) int32

// VaultRequest calls an allowlisted vault service. Returns the response
// body. If the capability did not declare vault = ["<service>"], this call
// does not link; if the service is not in the allowlist at runtime, rc=-2.
func VaultRequest(service, path string) ([]byte, error) {
	n := hostVaultRequestLen(
		unsafe.StringData(service), uint32(len(service)),
		unsafe.StringData(path), uint32(len(path)),
	)
	if n < 0 {
		return nil, hostError("vault.request", n)
	}
	if int(n) > len(ioBuf) {
		return nil, hostError("vault.request: response exceeds SDK buffer", -5)
	}
	got := hostVaultRequest(
		unsafe.StringData(service), uint32(len(service)),
		unsafe.StringData(path), uint32(len(path)),
		&ioBuf[0], uint32(len(ioBuf)),
	)
	if got < 0 {
		return nil, hostError("vault.request", got)
	}
	out := make([]byte, got)
	copy(out, ioBuf[:got])
	return out, nil
}

// VaultRequestJSON is a convenience wrapper that decodes the response into v.
func VaultRequestJSON(service, path string, v any) error {
	body, err := VaultRequest(service, path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// -----------------------------------------------------------------------------
// App helpers — extract method/path/body from env + stdin (CGI-style).
// This mirrors what serveCmd sets in alf-wasm-host.
// -----------------------------------------------------------------------------

// Request is the simplified HTTP request view exposed to app guests.
type Request struct {
	Method string
	Path   string
	Body   []byte
}

// ReadRequest reads the current request from env vars + stdin.
// Safe to call in any app guest; for tools it returns an empty Request.
func ReadRequest() Request {
	r := Request{
		Method: os.Getenv("ALF_METHOD"),
		Path:   os.Getenv("ALF_PATH"),
	}
	if nStr := os.Getenv("ALF_BODY_LENGTH"); nStr != "" {
		if n, err := strconv.Atoi(nStr); err == nil && n > 0 {
			r.Body = make([]byte, n)
			_, _ = os.Stdin.Read(r.Body)
		}
	}
	return r
}

// WriteResponse writes the body to stdout. The host reads stdout as the
// response body. Response status is signalled by the process exit code:
// 0 = 200 OK, non-zero = 500. A richer contract would use a header
// section; the spike keeps it minimal.
func WriteResponse(body []byte) {
	os.Stdout.Write(body)
}

// WriteResponseString is a shortcut for WriteResponse.
func WriteResponseString(s string) {
	os.Stdout.Write([]byte(s))
}

// -----------------------------------------------------------------------------
// Memory + Events (stubs)
// -----------------------------------------------------------------------------

//go:wasmimport alf memory_remember
func hostMemoryRemember(ptr *byte, length uint32) int32

// MemoryRemember stores text in long-term memory. Stubbed in the spike —
// real runtime wires to memstore.
func MemoryRemember(text string) error {
	rc := hostMemoryRemember(unsafe.StringData(text), uint32(len(text)))
	if rc < 0 {
		return hostError("memory.remember", rc)
	}
	return nil
}

//go:wasmimport alf events_emit
func hostEventsEmit(kindPtr *byte, kindLen uint32, payloadPtr *byte, payloadLen uint32) int32

// EventsEmit sends an event to the host event bus.
func EventsEmit(kind string, payload []byte) error {
	var pp *byte
	if len(payload) > 0 {
		pp = &payload[0]
	} else {
		pp = &ioBuf[0]
	}
	rc := hostEventsEmit(
		unsafe.StringData(kind), uint32(len(kind)),
		pp, uint32(len(payload)),
	)
	if rc < 0 {
		return hostError("events.emit", rc)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Error mapping
// -----------------------------------------------------------------------------

// Error encapsulates host-return-code failures.
type Error struct {
	Op   string
	Code int32
}

func (e *Error) Error() string {
	switch e.Code {
	case -1:
		return e.Op + ": not found"
	case -2:
		return e.Op + ": permission denied (manifest did not grant it)"
	case -3:
		return e.Op + ": buffer too small"
	case -4:
		return e.Op + ": invalid argument"
	case -5:
		return e.Op + ": host internal error"
	default:
		return e.Op + ": host rc=" + strconv.Itoa(int(e.Code))
	}
}

func hostError(op string, code int32) error {
	return &Error{Op: op, Code: code}
}
