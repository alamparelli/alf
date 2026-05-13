package wasm

import (
	"net/http"
	"time"
)

// DefaultHTTPClientTimeout caps a single alf_http_request call (including
// TLS handshake + body upload + server processing + body download). The
// ABI in this wave is byte-buffered (no streaming) so a single hung
// request would block the guest until this timeout fires.
const DefaultHTTPClientTimeout = 30 * time.Second

// NewDefaultHTTPClient returns the *http.Client the daemon hands to
// Instantiator.WithHTTPClient for the #421 outbound HTTP path. It is a
// vanilla client with a 30 s end-to-end timeout. The Transport field
// is left zero so the client uses http.DefaultTransport, which reads
// HTTP_PROXY / HTTPS_PROXY at request time via http.ProxyFromEnvironment
// — the daemon sets these env vars at boot (cmd/alf-daemon/main.go:287)
// to point at the firewall HTTP proxy on 127.0.0.1:4751
// (internal/sandbox/network/proxy.go). So every alf_http_request call
// transparently traverses the operator-managed allow/deny rules and
// is logged in the firewall request ring buffer.
//
// Three layers of egress control in 0.8.x:
//
//   1. WASM isolation (wazero) — the guest has no other way to make a
//      network call. wasi:sockets is forbidden in [[raw_imports]].
//   2. Scope-at-handle (HTTPScope.Allows) — the manifest's declared
//      [[http.scopes]] is the per-bundle allowlist. Out-of-scope =
//      errOutOfScope, no net.Dial.
//   3. Firewall (HTTP_PROXY) — the operator's domain-level rules
//      apply globally across every daemon-originated request.
//
// The *http.Client seam stays here so future waves can swap in:
//   - a vault-proxy-aware client for [[secrets.scopes]] (0.9.0+),
//     where Authorization headers are injected server-side via the
//     <contextDir>/vault-llm.sock so the guest never sees the token;
//   - a TLS-pinned client for high-assurance scenarios;
// — without rewriting host_http.go.
func NewDefaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: DefaultHTTPClientTimeout,
		// Refuse to follow redirects automatically. The forge mints
		// HTTPScope from the manifest at load time; an automatic
		// redirect would silently send the request to a host the
		// manifest did not declare. Wave 2 returns the redirect
		// response as-is and lets the guest decide whether to
		// re-issue (which it can only do if the target host is also
		// declared, satisfying HTTPScope.Allows).
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
