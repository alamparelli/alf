package wasm

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// TestClassifyHTTPError pins the host-ABI error-code mapping for
// HTTPHandle.Do failures. The guest sees these codes in the high
// 32 bits of the packResult return; the mapping is the bridge.
func TestClassifyHTTPError(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want uint32
	}{
		{"revoked", handle.ErrRevoked, errRevoked},
		{"out of scope", handle.ErrOutOfScope, errOutOfScope},
		{"unknown", errors.New("connection refused"), errIO},
		{"tls record header error", tls.RecordHeaderError{}, errHTTPTLSFailure},
		{"x509 error message", errors.New(`x509: certificate signed by unknown authority`), errHTTPTLSFailure},
		{"tls handshake error message", errors.New(`tls: handshake failure`), errHTTPTLSFailure},
		{"certificate error message", errors.New(`certificate has expired or is not yet valid`), errHTTPTLSFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyHTTPError(c.in); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

// TestParseHeadersInto_HappyPath pins the CRLF-delimited header parser.
// Each "Name: Value" line becomes a Header.Add — duplicates preserved,
// names canonicalised by net/http.
func TestParseHeadersInto_HappyPath(t *testing.T) {
	dst := http.Header{}
	raw := []byte("User-Agent: alf/0.8.0\r\nAccept: application/json\r\nX-Trace-Id: abc123\r\n")
	if err := parseHeadersInto(dst, raw); err != nil {
		t.Fatalf("parseHeadersInto: %v", err)
	}
	if got := dst.Get("User-Agent"); got != "alf/0.8.0" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := dst.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if got := dst.Get("X-Trace-Id"); got != "abc123" {
		t.Errorf("X-Trace-Id = %q", got)
	}
}

// TestParseHeadersInto_DuplicateNamesPreserved pins multi-value
// support (e.g. Set-Cookie style request mirroring).
func TestParseHeadersInto_DuplicateNamesPreserved(t *testing.T) {
	dst := http.Header{}
	raw := []byte("X-Custom: a\r\nX-Custom: b\r\n")
	if err := parseHeadersInto(dst, raw); err != nil {
		t.Fatalf("parseHeadersInto: %v", err)
	}
	if got := dst["X-Custom"]; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("X-Custom = %v, want [a b]", got)
	}
}

// TestParseHeadersInto_Empty is a no-op.
func TestParseHeadersInto_Empty(t *testing.T) {
	dst := http.Header{}
	if err := parseHeadersInto(dst, nil); err != nil {
		t.Errorf("nil bytes: %v", err)
	}
	if err := parseHeadersInto(dst, []byte{}); err != nil {
		t.Errorf("empty bytes: %v", err)
	}
	if len(dst) != 0 {
		t.Errorf("dst should remain empty, got %v", dst)
	}
}

// TestParseHeadersInto_MalformedRejected pins that lines without a
// colon (or with empty names) are refused. A malformed guest must not
// silently drop authentication metadata — the dispatch returns errIO
// so the guest sees the failure rather than a silent header omission.
func TestParseHeadersInto_MalformedRejected(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("NoColonAtAll\r\n"),
		[]byte(": value-with-no-name\r\n"),
	} {
		dst := http.Header{}
		err := parseHeadersInto(dst, raw)
		if !errors.Is(err, errMalformedHeader) {
			t.Errorf("input=%q: got %v, want errMalformedHeader", raw, err)
		}
	}
}

// TestParseHeadersInto_SkipsEmptyLines pins that an extra trailing CRLF
// (or any blank line) does NOT count as malformed — common in
// hand-rolled guest buffers and harmless.
func TestParseHeadersInto_SkipsEmptyLines(t *testing.T) {
	dst := http.Header{}
	raw := []byte("Accept: */*\r\n\r\n")
	if err := parseHeadersInto(dst, raw); err != nil {
		t.Errorf("trailing CRLF should not fail: %v", err)
	}
	if got := dst.Get("Accept"); got != "*/*" {
		t.Errorf("Accept = %q", got)
	}
}

// TestBuildHTTPRequest_HappyPath confirms the request is built with
// the ctx bound, the method upper-cased, and the headers attached.
func TestBuildHTTPRequest_HappyPath(t *testing.T) {
	ctx := context.Background()
	req, err := buildHTTPRequest(ctx,
		"get",
		"https://openlibrary.org/api/books",
		[]byte("X-Trace: 1\r\n"),
		nil,
	)
	if err != nil {
		t.Fatalf("buildHTTPRequest: %v", err)
	}
	if req.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", req.Method)
	}
	if req.URL.String() != "https://openlibrary.org/api/books" {
		t.Errorf("URL = %q", req.URL.String())
	}
	if req.Header.Get("X-Trace") != "1" {
		t.Errorf("X-Trace header lost: %v", req.Header)
	}
}

// TestBuildHTTPRequest_EmptyMethodDefaultsToGET pins the documented
// fallback: a guest that passes an empty method string gets GET.
func TestBuildHTTPRequest_EmptyMethodDefaultsToGET(t *testing.T) {
	req, err := buildHTTPRequest(context.Background(),
		"", "https://example.com", nil, nil)
	if err != nil {
		t.Fatalf("buildHTTPRequest: %v", err)
	}
	if req.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", req.Method)
	}
}

// TestBuildHTTPRequest_BodyAttached pins that a non-empty body slice
// is passed through. The body is a fresh copy on the host side per
// readGuestBytes; this test just verifies the wiring.
func TestBuildHTTPRequest_BodyAttached(t *testing.T) {
	body := []byte(`{"q":"alf"}`)
	req, err := buildHTTPRequest(context.Background(),
		"POST", "https://example.com/search",
		[]byte("Content-Type: application/json\r\n"),
		body,
	)
	if err != nil {
		t.Fatalf("buildHTTPRequest: %v", err)
	}
	if req.Body == nil {
		t.Fatal("body not set")
	}
	if req.ContentLength != int64(len(body)) {
		// http.NewRequest sets ContentLength when the body is *bytes.Reader.
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(body))
	}
}
