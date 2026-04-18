# Spike — WASM capabilities runtime for ALF

## Goal

Prove end-to-end that a WASM-based capability runtime can replace ALF's custom
sandboxing stack (chroot bash scripts, CAP_SYS_ADMIN daemon, integrity guard,
4 parallel permission systems) with a single `manifest → host imports → gated
execution` model.

## Scope

- Self-contained module under `experimental/wasm/`. Zero modification outside.
- Own `go.mod`. Deps: `wazero` (pure Go, no CGo).
- One **host runtime** that loads `.wasm` guests with policy-gated host imports.
- One **SDK** (Go) that guests use to call host capabilities.
- Two **examples**: a hello-world tool and a hello-world app, both built via the SDK.
- A **CLI** (`alf-wasm-host`) to run tools and serve apps in dev.
- A **Makefile** with `setup`, `build`, `demo` targets.

## Non-goals

- No integration with `cmd/alf-daemon/main.go`.
- No migration of `internal/tooling/native_*.go`.
- No marketplace/OCI distribution.
- No JS or Python runtime yet (Go→WASM is the minimum viable demonstration).
- No cosign signature verification (stubbed, documented).

## Success criteria

- [ ] `make setup && make build && make demo-tool` runs the hello tool
      end-to-end, prints its output, confirms storage round-trip.
- [ ] `make demo-app` starts the hello app, user can curl `localhost:8787/`
      and see an HTML response backed by guest logic + host storage.
- [ ] Policy enforcement demo: running a guest that tries to call an
      undeclared host capability returns a clear "not permitted" error, NOT
      an unhandled trap.
- [ ] Cold start measured for the tool: `bench/cold_start_test.go` reports
      p50 and p95 values.
- [ ] `strace -f -e trace=openat,connect,execve` of the host while the guest
      runs shows **zero** syscalls originating from the guest module that
      were not routed through a declared host import.

## Kill criteria

Any ONE triggers abort (write `POSTMORTEM.md`, archive branch):

- Cold start > 100 ms for a trivial Go tool after first-call warm-up.
- wazero blocker: missing feature on wasip1 that prevents basic I/O.
- Guest cannot write anything larger than 64 KB (memory protocol broken).
- Go→wasip1 build step fails on the user's Go toolchain.
- Memory per instance > 30 MB for hello-world.

## Timebox

2 calendar weeks from branch creation. At T+2w, decision: GO / NO-GO / PIVOT.

## Deliverables on merge (if GO)

A follow-up RFC PR that references this spike as evidence, proposing:

1. Adoption of the ABI (WIT interfaces: `log`, `storage`, `vault`, `memory`,
   `tools`, `events`, `http`).
2. Integration path into `internal/runtime/` (new package per
   `technical/ARCHITECTURE-v0.7.10.md`).
3. Language roadmap: Go now, JS (QuickJS) next, Python (Pyodide) after.
4. Classe C plan: which native binaries stay (ffmpeg/claude CLI/pdftotext)
   and how they become host services behind `vault.request`-style imports.

## Out of this spike

The spike proves the **pattern**. A production rewrite of ALF's sandbox around
WASM is ~6-10 weeks of focused work *after* the spike validates.
