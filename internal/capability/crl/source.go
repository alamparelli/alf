// Package crl carries the offline cache + periodic refresher that
// pulls signed CRLs from alf release infrastructure and applies them
// to the daemon's trust store. Sits above internal/capability/envelope/
// (which owns the wire format + verification).
//
// Spec: docs/ARCHITECTURE-SECURITY.md §7.7 + §8 — daemon caches the
// last-known-good CRL on disk. After N days offline, warns the user
// but continues operating (fail-safe).
package crl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Source produces a signed-CRL JSON blob (the SignedCRL wire form per
// envelope.EncodeSignedCRL). Implementations decide where the bytes
// come from — production: HTTPSource over the alf release CDN; tests:
// MockSource pinning specific responses.
type Source interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// ErrSourceUnavailable signals a transport-level failure — network
// down, HTTP 5xx, etc. The Refresher treats this as "offline; keep
// operating with the cached CRL". Distinct from ErrSourceMalformed
// where the response arrived but isn't a CRL.
var ErrSourceUnavailable = errors.New("crl: source unavailable")

// ErrSourceMalformed signals a response that arrived but cannot be
// interpreted as a signed CRL (bad JSON, signature mismatch handled
// upstream by ParseSignedCRL). Refresher does NOT fall back to the
// cache for malformed responses — a CRL the upstream actively
// served wrong is a stronger signal than "didn't reach us".
var ErrSourceMalformed = errors.New("crl: source malformed")

// HTTPSource fetches the signed CRL JSON from a URL over HTTPS. The
// caller is responsible for the URL — the alf release infra publishes
// it at a stable path. URL is required; Client falls back to a 30s
// timeout default if nil.
type HTTPSource struct {
	URL    string
	Client *http.Client
}

// MaxCRLBytes caps the response body to defend against a hostile or
// misconfigured upstream serving multi-GB. 4 MiB is comfortably above
// any realistic CRL size (entries are ~64 bytes each → 60k entries
// before we cap).
const MaxCRLBytes = 4 << 20

// Fetch implements Source. Returns ErrSourceUnavailable on transport
// errors and non-2xx status codes; ErrSourceMalformed on body read
// failures (which suggest a truncated/corrupt response). Body is
// capped at MaxCRLBytes.
func (h *HTTPSource) Fetch(ctx context.Context) ([]byte, error) {
	if h.URL == "" {
		return nil, fmt.Errorf("%w: empty URL", ErrSourceUnavailable)
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrSourceUnavailable, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrSourceUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxCRLBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrSourceMalformed, err)
	}
	if int64(len(body)) >= MaxCRLBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrSourceMalformed, MaxCRLBytes)
	}
	return body, nil
}
