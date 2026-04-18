# DELETIONS — WASM migration roadmap

> This document is the **plan**, not the act. Each row describes a deletion
> or simplification that becomes safe only after the corresponding wazero
> runtime integration has taken its place. The build stays green at each
> step.

## State today (snapshot on `spike/wasm` at commit `1a11c5a` + this PR)

- `experimental/wasm/` — standalone POC, validated end-to-end
- `internal/runtime/wasm/` — integration point inside the main module,
  with compile cache (4 ms warm vs spike's 700 ms), Notifier interface,
  pluggable VaultClient, full unit + bench test coverage
- **Nothing in the legacy sandbox stack has been removed yet** — all code
  still compiles, all non-preexisting tests still pass

## Count of what the migration eliminates

Numbers below are `wc -l` of the current files. They exclude whitespace.
Deletions include both code and the tests that test only that code.

### Bucket A — the custom subprocess sandbox (pure delete, 1950 LoC)

| File | LoC | Why it dies |
|---|---:|---|
| `internal/tooling/sandbox.go` | 61 | `CheckBoundary` → covered by wazero's memory model + per-capability storage root |
| `internal/tooling/sandbox_linux.go` | 366 | 140 L of bash-in-Go doing `mount --rbind` + chroot + setpriv — replaced by wazero's WASI sandbox (zero syscall surface) |
| `internal/tooling/sandbox_other.go` | 88 | stub for non-Linux — not needed |
| `internal/tooling/sandbox_test.go` | 379 | tests the deleted code |
| `internal/tooling/sandbox_linux_test.go` | 550 | tests the deleted bash+chroot ceremony |
| `internal/tooling/credential_linux.go` | 18 | `syscall.Credential{Uid:1000}` for subprocess drop — no subprocess, no uid crossing |
| `internal/tooling/credential_other.go` | 9 | stub, idem |
| `internal/tooling/sandbox_security_test.go`¹ | varies | any SEC-prefixed test exercising the bash sandbox |
| **subtotal** | **≈ 1 471** | |

¹ file doesn't exist under that name today; listed for completeness — check
against `rg "SEC-" internal/tooling/`.

### Bucket B — integrity guard (pure delete, 1100 LoC)

| File | LoC | Why it dies |
|---|---:|---|
| `internal/tooling/integrity.go` | 641 | hash-based tamper detection for drop-in shell tools. WASM capabilities are signed at build, verified at load (cosign). Polling + quarantine dir become obsolete. |
| `internal/tooling/integrity_test.go` | ≈ 400 | tests the deleted integrity code |
| `.daemon/tool-manifest.json` on disk | — | runtime artefact, not code |
| `.daemon/tool-quarantine/` on disk | — | runtime artefact |
| **subtotal** | **≈ 1 040** | |

### Bucket C — IPC back-channels for subprocess tools (delete, 480 LoC)

| File | LoC | Why it dies |
|---|---:|---|
| `internal/signal/` (server + Unix socket) | ≈ 200 | replaced by `Notifier.GuestLog` + direct host function calls |
| `internal/controlcenter/tools_proxy.go` | ≈ 150 | `ALF_TOOLS_SOCK` — gone, no subprocess to call back into CC |
| `cmd/signal/main.go` | ≈ 100 | separate binary subsumed into daemon in-process |
| `main.go: persistentSigServer` (lines ≈ 749–779) | ≈ 30 | signal socket bootstrap disappears |
| **subtotal** | **≈ 480** | |

### Bucket D — subprocess binaries collapsed into host functions (delete)

These whole `cmd/*` packages become host imports inside the daemon. No
separate binary, no socket, no auth token.

| Package | Approx LoC | Replacement |
|---|---:|---|
| `cmd/system-tools/` | varies | `tools.invoke` host import |
| `cmd/memory-tools/` | varies | `memory.remember/recall` host imports |
| `cmd/schedule-tools/` | varies | `schedule.*` host imports |
| `cmd/signal/` | varies | (see Bucket C) |

### Bucket E — simplifications (not deletions)

These files shrink significantly; they do not disappear.

| File | Today | After | Saving |
|---|---:|---:|---:|
| `cmd/alf-daemon/main.go` | 2168 | ~1700 | -470 (symlink setup, tools-proxy bootstrap, alfCred, signal socket) |
| `internal/tooling/registry.go` | — | — | integrity field removed |
| `internal/tooling/executor.go` | — | — | sandbox wiring removed; native path dispatches through `runtime/wasm` |
| `internal/tooling/native_*.go` (~20 files) | ~6 500 | ~4 000 | thin host-function adapters; lose `exec.Command` + `safeEnv` + shell quoting |
| `internal/marketplace/permissions.go` | 86 | ~30 | permission list collapses into manifest `world` — keeps name/slug/version validation only |
| `internal/controlcenter/handler_app*.go` | ~1 300 | ~200 | no iframe+port mapping, no per-app token, no proxy — `runtime.InvokeApp` does the work |
| `internal/controlcenter/handler_bash.go` | 231 | 0 (delete) | apps do not have raw `bash`; the LLM agent's bash goes through a single nsjail-backed host function |
| `scripts/entrypoint.sh` uid dance | — | -60 | no dual-uid for tools/apps; only kept for Classe C bins |

### Bucket F — container config (operational simplifications)

| Item | Today | After |
|---|---|---|
| `CAP_SYS_ADMIN` on daemon | granted | **removed** |
| `CAP_SYS_CHROOT` on daemon | granted | **removed** |
| `apparmor=unconfined` on container | required | **removed** (default Docker AppArmor re-enabled) |
| User `alf` uid 1000 | used for every subprocess | only for Classe C native bins (ffmpeg/whisper/claude CLI) |
| `/opt/alf/tools.d/` symlink dance in `main.go` | present | gone |

### Net code delta

| Category | Deleted | Simplified (delta) |
|---|---:|---:|
| Bucket A (sandbox) | -1 471 | — |
| Bucket B (integrity) | -1 040 | — |
| Bucket C (IPC) | -480 | — |
| Bucket D (cmd/ binaries) | -600 (5 packages) | — |
| Bucket E (handler/main shrink) | — | -2 500 |
| `internal/runtime/wasm/` (new) | +900 | — |
| `experimental/wasm/` (retired once integrated) | -1 800 | — |
| **Net** | **≈ -4 500** | **≈ -2 500** |

**Rough total: -6 000 to -7 000 lines of code. -3 binaries. -3 caps.
-1 AppArmor exemption.**

## Migration order (each is its own commit, tests green at each step)

### Phase 1 — **done in this PR**
- [x] Port the spike runtime into `internal/runtime/wasm/`
- [x] Add wazero + BurntSushi/toml to main `go.mod`
- [x] Compile cache → warm invocation ~4 ms
- [x] Notifier + VaultClient injection points
- [x] Integration test reuses experimental/wasm's `.wasm` artefacts

### Phase 2 — **next commit, ~2 days**
- [ ] Integrate the `runtime/wasm` package behind `internal/tooling/Executor`
      as an alternative path for native tools that opt in
      (no removal yet; `ruleset.json` toggle)
- [ ] Migrate 1 native tool as a proof-point (candidate: `native_log` —
      trivial, no subprocess)

### Phase 3 — **2-3 days**
- [ ] Add `ai.claude-query` and `media.*` host functions that route to
      Classe C native bins via a tight nsjail wrapper (the small helper
      replaces `sandbox_linux.go`'s 140 L of bash with ~40 L of nsjail config)
- [ ] Wire `env.shell.execute` for the LLM agent loop, gated on caller
      trust tier

### Phase 4 — **1 week**
- [ ] Migrate all 20 `native_*.go` tools to dispatch through the WASM host
      function registry (where it makes sense) or stay as pure Go
      host functions (for primitives like `storage`, `log`)
- [ ] Remove `exec.Command`/`safeEnv` infrastructure from those paths

### Phase 5 — **2-3 days**
- [ ] Migrate `controlcenter/handler_app*.go` to use `runtime.InvokeApp`
- [ ] Drop the iframe-port-mapping and per-app token dance

### Phase 6 — **1 day: the actual deletions**
- [ ] Delete files in Bucket A
- [ ] Delete files in Bucket B
- [ ] Delete files in Bucket C
- [ ] Delete `cmd/*` packages in Bucket D
- [ ] Shrink Bucket E files
- [ ] Edit Dockerfile/entrypoint.sh to drop caps
- [ ] Retire `experimental/wasm/` (becomes redundant with `internal/runtime/wasm/`)

### Phase 7 — **follow-up PR**
- [ ] JS runtime (QuickJS/javy) for LLM-authored tools & apps in JS
- [ ] Python runtime (Pyodide) for LLM-authored Python tools
- [ ] Cosign signature verification at module load
- [ ] WIT / Component Model adoption (optional, when WASI 0.2 ecosystem is mature)

## What is deliberately NOT in this plan

- Touching `internal/memstore/` (SQLite + ONNX stays native — no WASM port)
- Touching `internal/vault/` (the existing vault-proxy is the VaultClient impl)
- Touching `internal/firewall/` (still useful for Classe C binaries)
- Touching `internal/provider/` / `internal/router/` / `internal/agents/`
  (these are the AI domain, orthogonal to sandbox)
- Replacing the Svelte frontend (not involved in the security story)

## Total timeline

**Phase 1 (done) + Phase 2 → Phase 6: 2-3 weeks of focused work.**

Phase 7 is optional expansion, not required for the simplification thesis.
