// Package admin hosts the alf CLI subcommands that grow or modify
// the trust surface of the system: alf trust add/list/remove/revoke,
// alf keygen, alf sign, alf pending, alf ratify (the latter four
// land in subsequent #395 Stage 2 chunks).
//
// Per ARCHITECTURE-SECURITY §6 + #395, this package exists so the
// build-time archtest can pin a one-line invariant: nothing under
// internal/runtime/, internal/tooling/, internal/capability/handle/,
// or any LLM-driven HTTP route may import it. The trust-mutating
// surface is reachable only from the CLI binary's main and from the
// future CC /admin/ratify trust domain.
//
// Stage 2 chunk 1 (this drop): the trust subcommands. They mutate
// <dataDir>/trust/ directly via DirTrustStore.Persist /
// PersistRemove / PersistRevoke — no daemon roundtrip — so prompt-
// injection on the daemon's HTTP surface cannot reach trust state.
// The running daemon picks up changes on restart (or on a future
// SIGHUP-driven Load() — deferred until the wiring lands).
package admin
