// Package wasm is the integration point for ALF's WASM capability runtime.
//
// It absorbs the standalone spike under experimental/wasm/internal/host into
// the main module so ALF subsystems (daemon, control center, scheduler) can
// invoke WASM-sandboxed tools and apps in place of the legacy subprocess
// sandbox (tooling/sandbox*.go + integrity.go + the custom chroot/setuid
// ceremony in cmd/alf-daemon/main.go).
//
// The migration plan is documented in experimental/wasm/DELETIONS.md. This
// package exists to host the replacement; nothing removes the legacy stack
// until each subsystem is migrated one commit at a time with tests green.
//
// Key additions over the spike:
//
//   - A compile cache keyed by wasm file hash, so a warm invocation is
//     ~1-10 ms instead of the spike's ~700 ms cold path.
//   - Dependency injection for the HTTP client (so real ALF can route vault
//     traffic through its existing vault-proxy rather than hitting the
//     internet directly).
//   - A pluggable notifier interface for host-side logging, replacing the
//     spike's direct log.Printf calls.
package wasm
