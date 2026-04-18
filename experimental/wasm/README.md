# ALF WASM spike

A **self-contained** experiment validating a WASM-based capability runtime as
the replacement for ALF's current custom sandboxing stack (chroot via bash,
`CAP_SYS_ADMIN` daemon, integrity guard, 3 parallel permission systems).

Nothing outside `experimental/wasm/` is modified. This is a **pattern proof**,
not an integration.

## What it demonstrates

1. A host runtime (`alf-wasm-host`, Go + [`wazero`](https://wazero.io)) that
   loads guest `.wasm` modules with **manifest-gated** host imports.
2. A Go SDK (`sdk/go/alf`) that guests import to call host capabilities
   (`log`, `storage`, `vault`, `memory`, `events`).
3. A **tool** (hello-world) and an **app** (hello-world with frontend),
   both built from `.go` source through `GOOS=wasip1 GOARCH=wasm go build`.
4. **Policy enforcement**: capabilities not declared in the manifest are
   structurally absent from the linked module (link-time failure), and
   per-service allowlists are enforced in host function bodies (rc=-2 at
   runtime).

## Prerequisites

- Go ≥ 1.24 (you have 1.25.x — perfect)
- A shell with `make`
- Nothing else: no toolchain install, no Docker, no external daemon

## Quickstart

```bash
cd experimental/wasm
make setup
make demo-tool         # runs the hello tool twice; counter persists in _data/
make demo-app          # serves hello app on http://127.0.0.1:8787
```

Open `http://127.0.0.1:8787` in a browser and click the buttons. The
interesting button is **/api/denied-demo** — it attempts a vault call to
`openai` (not declared in the manifest) and you will see the host return
code `-2` come back cleanly, without the guest crashing or bypassing.

## Directory map

```
experimental/wasm/
├── SPIKE.md                Goals, success/kill criteria, timebox
├── README.md               This file
├── Makefile                setup / build / demo targets
├── go.mod                  Host module (wazero + BurntSushi/toml)
│
├── cmd/alf-wasm-host/      CLI: run | serve
│   └── main.go
│
├── internal/host/          Runtime glue — the only code that knows wazero
│   ├── manifest.go         TOML manifest struct + parser
│   ├── policy.go           Manifest → Policy (single source of truth)
│   ├── storage.go          File-backed per-capability KV
│   ├── imports.go          All host imports exposed to guests
│   └── runtime.go          Load + instantiate + invoke a guest
│
├── sdk/go/alf/             Guest SDK — compiled into each .wasm
│   └── alf.go              //go:wasmimport wrappers, build tag wasip1
│
└── examples/
    ├── tool-hello/
    │   ├── manifest.toml   declares: log, storage
    │   └── main.go         writes to storage, reads back, logs, exits
    │
    └── app-hello/
        ├── manifest.toml   declares: log, storage, vault["coingecko","httpbin"]
        ├── main.go         /api/hello, /api/counter, /api/btc, /api/denied-demo
        └── frontend/
            └── index.html  static frontend calling /api/*
```

## The security model, in one picture

```
                       ┌──────────────────────────┐
                       │  manifest.toml           │
                       │    permissions = {...}   │
                       └──────────┬───────────────┘
                                  │
                                  ▼ PolicyFromManifest
                       ┌──────────────────────────┐
                       │  Policy (Go struct)      │
                       └──────────┬───────────────┘
                                  │
                                  ▼ BuildHostModule
        ┌─────────────────────────────────────────┐
        │  wazero "alf" module — ONLY permitted   │
        │  functions are registered.              │
        │                                          │
        │  e.g. if manifest.permissions.vault = [] │
        │       then vault_request is NOT exposed  │
        │       → guest fails to link at instan-   │
        │         tiation. Cannot execute at all.  │
        └─────────────────────────────────────────┘
                                  │
                                  ▼
                      ┌────────────────────────┐
                      │  guest.wasm executes   │
                      │  (Go → wasip1 target)  │
                      └────────────────────────┘
```

The guest has access to:

- exactly the host functions declared in its manifest
- its own scoped storage directory (`_data/<capability-name>/`)
- stdin/stdout/stderr (host reads them for CGI-style apps)

It has **no** access to:

- the host filesystem (wasip1 gives no mounts by default)
- network sockets (no WASI networking in preview 1)
- subprocesses (WASM has no `fork`/`exec`)
- the host's memory, env vars other than what we pass in, or other modules

## What this spike is NOT

- **Not** an integration with `cmd/alf-daemon/main.go`. That's a follow-up.
- **Not** a JS or Python runtime. Go→wasip1 is the minimum to demonstrate the
  pattern with zero extra toolchain. Adding QuickJS/Pyodide comes next if
  this validates.
- **Not** a Component Model / WIT setup. We use wasip1 + hand-written Go
  imports; the upgrade path to WIT + Component Model is orthogonal.
- **Not** signed / distributed. No cosign verification, no OCI registry —
  those would be a separate concern for the production design.

## Kill criteria (see SPIKE.md)

If any of these hit, we write `POSTMORTEM.md`, tag the branch, and revisit:

- Cold start > 100 ms for the tool after first-call warm-up
- wazero blocker on wasip1
- Memory per instance > 30 MB
- Go→wasip1 build fails

## Next step (if GO)

Write an RFC PR proposing adoption — referencing this spike and
`technical/ARCHITECTURE-v0.7.10.md §2.4 (sandbox/)`. The RFC outlines the
6-10 week integration to replace `internal/tooling/sandbox*.go`,
`internal/tooling/integrity.go`, and the various socket-based IPC layers
with this runtime.
