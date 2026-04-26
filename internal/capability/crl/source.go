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
	"net"
	"net/http"
	"net/url"
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

// ErrInsecureURL signals SEC-007: ALF_CRL_URL was configured with a
// non-HTTPS scheme that is not a loopback address. Plaintext HTTP
// would let any net-position attacker swap the served CRL bytes —
// the Ed25519 signature still protects authenticity, but combined
// with SEC-001 replay it widens the rollback surface. Loopback
// (127.0.0.0/8, ::1, localhost) over HTTP is allowed for local dev
// and httptest harnesses.
var ErrInsecureURL = errors.New("crl: insecure URL — HTTPS required for non-loopback hosts")

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
// failures (which suggest a truncated/corrupt response); ErrInsecureURL
// if the URL scheme is HTTP and the host is not loopback (SEC-007).
// Body is capped at MaxCRLBytes.
func (h *HTTPSource) Fetch(ctx context.Context) ([]byte, error) {
	if h.URL == "" {
		return nil, fmt.Errorf("%w: empty URL", ErrSourceUnavailable)
	}
	if err := ValidateCRLURL(h.URL); err != nil {
		return nil, err
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

// ValidateCRLURL enforces SEC-007: the CRL URL must use HTTPS
// unless the host is loopback (127.0.0.0/8, ::1, or "localhost"),
// in which case HTTP is accepted to support local-dev and
// httptest harnesses. Empty / unparseable URLs return
// ErrSourceUnavailable.
//
// Exported so the daemon (cmd/alf-daemon/crl.go) can fail-fast at
// boot rather than waiting for the first refresher Tick to surface
// the misconfiguration.
func ValidateCRLURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%w: empty URL", ErrSourceUnavailable)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: parse URL: %w", ErrSourceUnavailable, err)
	}
	scheme := u.Scheme
	if scheme == "https" {
		return nil
	}
	if scheme != "http" {
		return fmt.Errorf("%w: scheme=%q (only http/https supported)", ErrInsecureURL, scheme)
	}
	// HTTP is allowed only for loopback hosts.
	host := u.Hostname()
	if isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%w: host=%q over plaintext http (use https://)", ErrInsecureURL, host)
}

// isLoopbackHost returns true for "localhost" or any IP literal
// inside the 127.0.0.0/8 IPv4 block or the ::1 IPv6 loopback. Used
// by ValidateCRLURL to allow HTTP only for local dev / test paths.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
