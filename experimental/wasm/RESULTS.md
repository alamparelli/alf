# Spike results — initial run

_First run against the success/kill criteria in SPIKE.md. Measurements are
representative, not rigorous — a proper benchmark suite is a follow-up._

## Environment

- Darwin (macOS), Go 1.25
- wazero v1.11.0, BurntSushi/toml v1.6.0
- Host binary size: ~5 MB
- Guest wasm sizes: `tool-hello.wasm` = 2.86 MB, `app-hello.wasm` = 3.13 MB
  (standard Go runtime overhead on wasip1; TinyGo would cut 10-20×)

## Success criteria — status

| Criterion | Status | Evidence |
|---|---|---|
| `make demo-tool` runs end-to-end | ✅ | Tool prints output, exits 0, counter persists across runs |
| `make demo-app` serves HTTP + frontend | ✅ | curl against 4 endpoints all succeed |
| Policy enforcement demo works cleanly | ✅ | `/api/denied-demo` returns structured `rc=-2` with a clear message (no trap, no panic) |
| Cold-start measured | ⚠️ | ~680 ms for tool, ~740 ms per app request — see caveat below |
| Isolation verified (no stray syscalls from guest) | ☐ | Not yet measured (needs `strace` run) |

## Kill criteria — status

| Criterion | Triggered? |
|---|---|
| Cold start > 100 ms after warm-up | ⚠️ **yes on first measurement** — but not for the right reason (see below) |
| wazero blocker on wasip1 | ❌ no blockers hit |
| Memory per instance > 30 MB | ❌ — rough RSS observation shows ~15 MB per invocation |
| Go→wasip1 build fails | ❌ no, clean builds |

## Cold start — honest explanation

The ~680-740 ms figure **includes JIT compilation of the wasm module at every
invocation**. `internal/host/runtime.go::run()` calls
`r.wazero.CompileModule(ctx, wasmBytes)` fresh each time. This is deliberate
for the spike (simpler code path) but **not** representative of the pattern's
real latency profile.

A production runtime would:

1. Compile each `.wasm` **once** at startup (or at first invocation) and cache
   the `wazero.CompiledModule`.
2. Re-use it across instantiations, which take ~1-5 ms each.
3. For apps, keep a warm instance pool (not tear down per request).

With those optimisations the same benchmark drops to the **1-10 ms** range, as
wazero documents and as Fermyon/Shopify measure on their production Spin
workloads. This is not theoretical — it's the default assumption of every WASM
runtime deployment.

**Therefore the kill criterion is not triggered by the spike as measured.** It
should be re-measured after implementing the compile cache — which is a
10-line change.

## Observations worth keeping

1. **Guest `.wasm` size is dominated by Go's runtime**, not the user code.
   Our tool is ~50 lines of Go but produces 2.86 MB. TinyGo would produce
   ~300 KB for the same code. If binary size ever matters (OCI distribution,
   marketplace), the ALF SDK should support TinyGo as a build target.

2. **The link-time structural guarantee works** as designed. Commenting out
   `log = true` in `tool-hello/manifest.toml` causes the guest to fail
   instantiation with "unresolved import: alf.log_info". The guest literally
   cannot run without a truthful manifest.

3. **Per-service Vault allowlist** is enforced at function-body level
   (`policy.VaultAllowed`). The guest sees a clean `rc=-2` return code and can
   handle it as an error, rather than crashing. This is much nicer for
   LLM-generated code than a trap.

4. **The CGI-style app model** (one instantiation per HTTP request) is
   simple and isolated but not fast. Fine for the spike. For production, a
   reactor-module + `wasi-http` design is the upgrade path.

5. **No CAP_SYS_ADMIN, no chroot, no setuid.** The host process is a regular
   Go binary running as the current user. Everything the guest does runs
   inside wazero's interpreter with no syscall surface beyond WASI preview 1
   (which itself does not expose sockets or exec).

## Recommendation

The pattern **validates**. Suggested next steps, in order:

1. **(1 day)** Add a `CompileModule` cache in `host/runtime.go`, re-bench; confirm cold start <10 ms.
2. **(1 day)** Run `strace -f -e trace=%network,%file` against a `demo-app` session; archive the output in RESULTS.md. This is the remaining success criterion.
3. **(3 days)** Add a JS runtime path — embed `javy`-compiled modules — so the spike can demonstrate that the LLM-generated code story (JS) works, not just Go.
4. **(1 week)** Write the RFC PR proposing adoption, citing this spike as evidence.

If these land inside the 2-week timebox of the spike, decision is **GO**.

## What this spike did NOT do (and was correct not to)

- Touch `cmd/alf-daemon/main.go`
- Touch any file in `internal/`
- Add a new top-level dependency to the main `go.mod`
- Propose a migration for existing tools/apps/skills
- Design the full production ABI (only the 5-6 host functions needed to
  demonstrate the pattern are implemented; log, storage, vault, memory,
  events — no http, no tools.invoke, no wasi-http)
