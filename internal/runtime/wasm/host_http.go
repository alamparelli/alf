package wasm

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tetratelabs/wazero/api"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// HTTP host-ABI error codes (extension of host_fs.go's space). The
// guest receives one of these in the packed-i64 result of
// alf_http_request. Sharing the numbering with the FS host imports
// keeps the guest's error-handling vocabulary unified across host
// imports — a single match statement covers every alf_* call.
//
// Numbering matches WASM.md §3.3:
//   0 = ok
//   1 = revoked              (lifecycle closed)
//   2 = out_of_scope         (URL not in manifest scope, or wrong scheme)
//   3 = io                   (network failure, unreadable guest mem, etc.)
//   4 = buffer_too_small     (out_body_max < response body size)
//   5 = http_status          (server returned non-2xx; body still in out)
//   6 = tls_failure          (TLS handshake, cert validation, or required-HTTPS denied)
const (
	errHTTPStatusNon2xx uint32 = 5
	errHTTPTLSFailure   uint32 = 6
)

// fnAlfHTTPRequest is the host function name the guest imports. WASM.md
// §3.4 documents the ABI in full; the short form:
//
//	(method_ptr, method_len,
//	 url_ptr, url_len,
//	 headers_ptr, headers_len,
//	 body_ptr, body_len,
//	 out_status_ptr,
//	 out_body_ptr, out_body_max) -> packResult(err, out_body_len)
//
// The headers buffer is a CRLF-delimited byte slice ("Name: Value\r\n"
// per header). Body is opaque bytes. Status is written to *out_status_ptr
// as a little-endian uint32. The packed i64 result follows host_fs.go
// conventions: high 32 bits = err code, low 32 bits = bytes written to
// out_body_ptr.
const fnAlfHTTPRequest = "alf_http_request"

// makeHTTPRequestDispatch returns the alf_http_request implementation.
// It looks up the calling guest's *handle.Instance via mod.Name() —
// if no Instance is registered or the Instance lacks an HTTPHandle,
// the call returns errIO. All guest memory access goes through
// api.Memory.Read / api.Memory.Write — never raw pointer arithmetic
// from guest-supplied offsets (the WASM.md §3.5 hygiene rule, pinned
// by the TestWASMHostHTTPUsesMemoryReadWriteOnly archtest).
func makeHTTPRequestDispatch(reg *hostHandleRegistry) func(
	ctx context.Context, mod api.Module,
	methodPtr, methodLen uint32,
	urlPtr, urlLen uint32,
	headersPtr, headersLen uint32,
	bodyPtr, bodyLen uint32,
	outStatusPtr uint32,
	outBodyPtr, outBodyMax uint32,
) uint64 {
	return func(
		ctx context.Context, mod api.Module,
		methodPtr, methodLen uint32,
		urlPtr, urlLen uint32,
		headersPtr, headersLen uint32,
		bodyPtr, bodyLen uint32,
		outStatusPtr uint32,
		outBodyPtr, outBodyMax uint32,
	) uint64 {
		inst := reg.Lookup(mod.Name())
		if inst == nil || inst.HTTP == nil {
			return packResult(errIO, 0)
		}

		mem := mod.Memory()
		method, ok := readGuestString(mem, methodPtr, methodLen)
		if !ok {
			return packResult(errIO, 0)
		}
		rawURL, ok := readGuestString(mem, urlPtr, urlLen)
		if !ok {
			return packResult(errIO, 0)
		}
		headerBytes, ok := readGuestBytes(mem, headersPtr, headersLen)
		if !ok {
			return packResult(errIO, 0)
		}
		bodyBytes, ok := readGuestBytes(mem, bodyPtr, bodyLen)
		if !ok {
			return packResult(errIO, 0)
		}

		req, err := buildHTTPRequest(ctx, method, rawURL, headerBytes, bodyBytes)
		if err != nil {
			return packResult(errIO, 0)
		}

		resp, err := inst.HTTP.Do(ctx, req)
		if err != nil {
			return packResult(classifyHTTPError(err), 0)
		}
		defer resp.Body.Close()

		// Write status code into guest memory (little-endian uint32).
		if !mem.WriteUint32Le(outStatusPtr, uint32(resp.StatusCode)) {
			return packResult(errIO, 0)
		}

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return packResult(errIO, 0)
		}
		if uint32(len(body)) > outBodyMax {
			return packResult(errBufferTooSmall, uint32(len(body)))
		}
		if !mem.Write(outBodyPtr, body) {
			return packResult(errIO, 0)
		}

		// Non-2xx is surfaced as a non-fatal status — the guest still
		// receives the body and the status code, but the err class
		// makes it trivial to branch on success in a single line.
		errCode := errOK
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errCode = errHTTPStatusNon2xx
		}
		return packResult(errCode, uint32(len(body)))
	}
}

// readGuestString reads len bytes from ptr in guest memory and returns
// them as a Go string. Returns (_, false) on out-of-bounds. The bytes
// are copied implicitly (string conversion materialises).
func readGuestString(mem api.Memory, ptr, length uint32) (string, bool) {
	if length == 0 {
		return "", true
	}
	b, ok := mem.Read(ptr, length)
	if !ok {
		return "", false
	}
	return string(b), true
}

// readGuestBytes reads len bytes from ptr in guest memory. Unlike
// readGuestString, the returned slice is a fresh copy of the guest
// memory window — the host may hold it across blocking calls
// (http.Client.Do) without risk that the guest resizes memory and
// invalidates the slice.
func readGuestBytes(mem api.Memory, ptr, length uint32) ([]byte, bool) {
	if length == 0 {
		return nil, true
	}
	b, ok := mem.Read(ptr, length)
	if !ok {
		return nil, false
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, true
}

// buildHTTPRequest constructs the *http.Request from the guest's
// inputs. Method and URL are validated minimally — full enforcement
// is the responsibility of HTTPHandle.Do (scope) and net/http (RFC
// compliance). Headers are parsed from CRLF-delimited bytes;
// duplicate names are concatenated per http.Header semantics. The
// caller-provided ctx is bound to the request so the request inherits
// the guest call's lifecycle.
func buildHTTPRequest(ctx context.Context, method, rawURL string, headerBytes, body []byte) (*http.Request, error) {
	if _, err := url.Parse(rawURL); err != nil {
		return nil, err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if err := parseHeadersInto(req.Header, headerBytes); err != nil {
		return nil, err
	}
	return req, nil
}

// parseHeadersInto walks a CRLF-delimited "Name: Value\r\n…" byte
// buffer and adds each entry to dst via Header.Add (case-insensitive,
// duplicates preserved per RFC 7230). Empty buffer is a no-op. Lines
// without a colon are rejected so a malformed guest does not silently
// drop authentication metadata.
func parseHeadersInto(dst http.Header, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	lines := strings.Split(string(raw), "\r\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			return errMalformedHeader
		}
		name := strings.TrimSpace(line[:i])
		value := strings.TrimSpace(line[i+1:])
		if name == "" {
			return errMalformedHeader
		}
		dst.Add(name, value)
	}
	return nil
}

// errMalformedHeader is intentionally local — guest header parsing
// failures all collapse to errIO from the guest's perspective.
var errMalformedHeader = errors.New("wasm: malformed header line in alf_http_request")

// classifyHTTPError maps handle-layer / transport errors to the host-
// ABI error codes per WASM.md §3.3. Specifically:
//   - handle.ErrRevoked   → errRevoked
//   - handle.ErrOutOfScope → errOutOfScope
//   - tls.RecordHeaderError or x509-related → errHTTPTLSFailure
//   - everything else      → errIO (transient network failure)
//
// Recognising the TLS class is best-effort — the std library surfaces
// it through several concrete types and string fragments. The first
// substring match wins. Misclassifying a TLS error as errIO is a
// usability bug, not a security bug.
func classifyHTTPError(err error) uint32 {
	switch {
	case errors.Is(err, handle.ErrRevoked):
		return errRevoked
	case errors.Is(err, handle.ErrOutOfScope):
		return errOutOfScope
	}
	var tlsRec tls.RecordHeaderError
	if errors.As(err, &tlsRec) {
		return errHTTPTLSFailure
	}
	msg := err.Error()
	if strings.Contains(msg, "tls:") ||
		strings.Contains(msg, "x509:") ||
		strings.Contains(msg, "certificate") {
		return errHTTPTLSFailure
	}
	return errIO
}
