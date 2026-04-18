# WASM integration — state at end of day 2026-04-19

> This document captures everything about the live spike integration.
> Start here tomorrow (or when returning to this work). Companion docs:
> [SPIKE.md](SPIKE.md) · [DELETIONS.md](DELETIONS.md) · [HOMELAB.md](HOMELAB.md) · [RESULTS.md](RESULTS.md).

## TL;DR

`spike/wasm` branch (pushed to origin, 8 commits, HEAD = `866df52`).

- **Deployed live** on homelab (image `alf-homelab:dev-866df52`), coexists
  with legacy shell sandbox (26 tools + ~30 apps intact).
- **End-to-end verified**:
  - Tool path: Gemma (OpenRouter) called `wasm_demo` → daemon → wazero →
    guest → host imports → JSON back.
  - App path: sandboxed iframe at `/apps/wasm-playground/` → AlfSDK.fetch →
    `/wasm-app/wasm-playground/api/*` → wazero → JSON back. **No tunnel.**
- **Build green, tests green** (non-preexisting failures only).

## Architecture — tool invocation

```
LLM (Gemma/Claude/Codex)
   │
   │ tool_call name="wasm_demo"
   ▼
tooling.Executor.Execute
   │
   │ natives[name].Run()  (native_wasm.go WASMTool adapter)
   ▼
internal/runtime/wasm.Runtime.InvokeTool
   │
   ├── manifest.toml → Policy (permissions)
   ├── compile cache (sha256-keyed CompiledModule) — 4ms warm vs 700ms cold
   └── wazero.InstantiateModule
         │
         │ WASI preview 1 _start()
         ▼
   guest.wasm (Go → GOOS=wasip1 GOARCH=wasm)
         │
         │ //go:wasmimport alf (log_info, storage_put, vault_request, …)
         ▼
   BuildHostModule filters by Policy — denied imports are absent from
   the linked module (link-time enforcement, not runtime check)
```

**Entry points**:
- Daemon init: `cmd/alf-daemon/main.go` (~60 L of WASM wiring after native tools)
- Runtime: `internal/runtime/wasm/*.go`
- Adapter to `NativeTool` interface: `internal/tooling/wasmtool.go`
- Guest SDK: `sdk/wasm/alf/alf.go` (build tag `wasip1`)

## Architecture — app invocation

```
Browser at https://cc.lamparelli.eu/apps/wasm-playground/
   │
   │ loads iframe (sandbox="allow-scripts", Origin: null)
   │
   │  AlfSDK.init() → MessageChannel handshake → app token _t
   ▼
   AlfSDK.fetch("/wasm-app/wasm-playground/api/hello")
   │   Authorization: Bearer <_t>
   │
   │ same-origin (no tunnel)
   ▼
CC HTTP mux (port 8080/443)
   │
   ├── loggingMiddleware
   ├── corsMiddleware  ── /wasm-app/ accepted for null-origin (if token valid)
   ├── securityHeadersMiddleware
   ├── rateLimiter
   ├── authMiddlewareWithAppTokens
   │   └── Bearer app token slug-scoped → token.slug must match URL slug
   ├── csrfMiddleware
   ├── appIsolationMiddleware
   ├── jsonMiddleware
   │
   │ (if all pass)
   ▼
Deps.ExtraHandlers["/wasm-app/"]  ← wasm.AppRouter
   │
   │ routes /wasm-app/{slug}/api/{rest} → InvokeApp(slug, method, path, body)
   ▼
wazero → same runtime pipeline as tool (compile cache, Policy-gated imports)
   │
   ▼
stdout bytes → HTTP 200 response body → browser
```

## The 4 categories on the homelab right now

| # | Category | Location | Example | How it got there |
|---|---|---|---|---|
| 1 | Go-native | `internal/tooling/native_*.go` | `bash`, `grep`, `read_file` | Part of the daemon binary |
| 2 | Shell legacy | `/home/alf/data/tools/*.{sh,py,bin}` | `postbro`, `ga`, `sonos` | Pre-existing, intact |
| 3 | WASM bundled | `wasm-guests/<name>/` in repo → embedded via `go:embed` → extracted at boot to `/home/alf/data/wasm-bundled/` | `wasm-demo` | `scripts/dev-deploy.sh` runs `wasm-guests/build.sh` |
| 4 | WASM user-placed | `/home/alf/data/{tools,apps}/<slug>/manifest.toml + .wasm` | `wasm-playground` | `scripts/deploy-wasm-playground.sh` |

Everything coexists in the same registries. Legacy is untouched.

## Files added / modified

### Added (codebase)
- `internal/runtime/wasm/` — `manifest.go`, `policy.go`, `storage.go`, `imports.go`, `runtime.go`, `app_handler.go`, `discovery.go`, `doc.go`, `runtime_test.go`, `bench_test.go`
- `internal/tooling/wasmtool.go` — adapter implementing `NativeTool`
- `cmd/alf-daemon/wasm_notifier.go` — log sink for guest GuestLog
- `sdk/wasm/alf/alf.go` — guest SDK (build tag `wasip1`)
- `wasm-guests/tool-demo/{main.go,manifest.toml}` — bundled reference tool
- `wasm-guests/embed.go`, `wasm-guests/build.sh` — go:embed plumbing
- `scripts/deploy-wasm-playground.sh` — one-shot playground deploy
- `experimental/wasm/` — SPIKE.md, DELETIONS.md, HOMELAB.md, RESULTS.md, README.md, this file

### Modified
- `cmd/alf-daemon/main.go` — ~60 L WASM bootstrap + extraCCHandlers wiring
- `internal/controlcenter/factory.go` — `Deps.ExtraHandlers` field, register in mux
- `internal/controlcenter/server.go` — `extraHandlers` param on `New()`
- `internal/controlcenter/middleware.go` — `/wasm-app/` accepted in CORS + auth slug-scope
- `go.mod` — `wazero v1.11.0`, `BurntSushi/toml v1.6.0`
- `scripts/dev-deploy.sh` — runs wasm-guests/build.sh before go build

## Deployment workflow

```bash
# Full redeploy (host + image + container on homelab)
bash scripts/dev-deploy.sh

# App-only redeploy (places manifest + wasm in container, restarts alf)
bash scripts/deploy-wasm-playground.sh
```

Verification after a deploy:

```bash
ssh alessandro@192.168.129.101 "docker logs --tail=60 alf 2>&1 | grep -iE 'wasm'"
```

Expected lines:

```
[wasm] registered tool "wasm-demo" (from /home/alf/data/wasm-bundled/tool-demo/manifest.toml)
[wasm-app] registered "wasm-playground" (frontend=false)
[wasm] discovery: 1 tool(s), 1 app(s) registered
tooling: loaded 27 tool schemas: [… wasm_demo …]
```

When an LLM invokes the tool:

```
tooling: executing tool wasm_demo args={"input": "…"}
[wasm:wasm-demo] info: wasm-demo invoked
[wasm:wasm-demo] info: wasm-demo done (run N)
toolloop: tool wasm_demo → 27 chars (error=false)
```

When a user clicks a button in the playground iframe:

```
[wasm:wasm-playground] info: app-hello handling GET /api/hello
[wasm-app] GET /wasm-app/wasm-playground/api/hello -> 200 (XXms)
```

## Adding a new WASM tool

### Option A — bundled (ships with every ALF install)

1. Create `wasm-guests/<name>/`:
   - `main.go` — build tag `wasip1`, imports `sdk/wasm/alf`
   - `manifest.toml` — declares name, kind="tool", permissions
2. Add the path to `wasm-guests/embed.go`'s `//go:embed` directive.
3. Redeploy — `bash scripts/dev-deploy.sh` rebuilds & ships.

### Option B — user-placed via SSH (per-instance, not in codebase)

Place directly on the container:

```bash
ssh alessandro@192.168.129.101 <<EOF
docker exec alf mkdir -p /home/alf/data/tools/<slug>
docker cp <built.wasm>  alf:/home/alf/data/tools/<slug>/<slug>.wasm
docker cp <manifest.toml> alf:/home/alf/data/tools/<slug>/manifest.toml
docker exec alf chown -R alf:alf /home/alf/data/tools/<slug>
docker restart alf
EOF
```

## Adding a new WASM app (with sidebar visibility)

Four files in `/home/alf/data/apps/<slug>/`:

| File | Purpose |
|---|---|
| `manifest.json` | Marketplace format → sidebar entry (`name`, `slug`, `category`, `icon`, `permissions`) |
| `manifest.toml` | WASM runtime format → registers backend (kind=app, entry, permissions) |
| `index.html` | Iframe content. MUST load `<script src="/static/alf-app-sdk.js"></script>` and use `AlfSDK.init({slug:'<slug>'})` + `AlfSDK.fetch()`. Raw `fetch()` will fail CORS/auth. |
| `<slug>.wasm` | Compiled guest (`GOOS=wasip1 GOARCH=wasm go build`) |

See `scripts/deploy-wasm-playground.sh` for a working template.

## CC security contract — what the WASM router inherits

Because `wasm.AppRouter` is mounted as an `ExtraHandler` **inside** the CC
mux (before the dashboard "/" catch-all), it gets the full middleware stack:

- **Auth** — Bearer app token via `extractAppBearerToken`, validated by `AppTokenStore`, slug-scoped (`/wasm-app/<slug>/*` requires a token whose slug matches).
- **CORS** — sandboxed iframes (`Origin: null`) are accepted on `/wasm-app/*` if the Bearer token validates.
- **Rate limit** — 15 req/min anonymous, 600/min authenticated.
- **Security headers** — HSTS, XFO, CSP, nosniff.
- **CSRF** — double-submit cookie for state-changing methods.

Audit ledger by path:

| Path | Auth required | Null-origin allowed | Slug-scoped |
|---|---|---|---|
| `/apps/<slug>/...` | session OR app token | ✅ | ✅ |
| `/api/apps/<slug>/...` | session OR app token | ✅ | ✅ |
| `/wasm-app/<slug>/...` | session OR app token | ✅ | ✅ (new) |
| `/static/...` | none | ✅ | — |
| everything else | session | varies | — |

## Guest ABI reference

Host module name: `alf`. Imports exposed per `Policy` derived from manifest.

```
log_info(ptr: *byte, len: u32)                                  // always logged via Notifier
log_error(ptr: *byte, len: u32)

storage_put(k_ptr, k_len, v_ptr, v_len: u32) -> i32             // rc: 0 | -1..-5
storage_get_len(k_ptr, k_len) -> i32
storage_get(k_ptr, k_len, out_ptr, out_cap) -> i32
storage_delete(k_ptr, k_len) -> i32

vault_request_len(svc_ptr, svc_len, path_ptr, path_len) -> i32  // needs service in manifest.permissions.vault
vault_request(svc_ptr, svc_len, path_ptr, path_len,
              out_ptr, out_cap) -> i32

http_fetch(url_ptr, url_len, out_ptr, out_cap) -> i32           // needs host in manifest.permissions.http

memory_remember(ptr, len) -> i32                                // stub, wires to memstore later
events_emit(kind_ptr, kind_len, payload_ptr, payload_len) -> i32  // stub
```

Return codes: `0`=ok, `-1`=not found, `-2`=denied, `-3`=buf too small, `-4`=bad arg, `-5`=host error.

Guest Go wrappers live in `sdk/wasm/alf/alf.go` — named `LogInfo`, `StoragePut/Get/Delete`, `VaultRequest`, `VaultRequestJSON`, `MemoryRemember`, `EventsEmit`, `ReadRequest`, `WriteResponse`.

## Known limitations (end of day)

1. **Codex doesn't see WASM tools.** Codex CLI runs as a subprocess and discovers tools via filesystem (`$PATH` + `toolbox.md`). The WASM registry lives in daemon memory. The fix is MCP (Model Context Protocol) server exposure — deferred.
2. **`DefaultVaultClient` hits public URLs directly.** The `/api/btc` button calls `api.coingecko.com` from the daemon process, not through the existing `vault-server`. Real integration = inject an ALF-specific `VaultClient` implementation that talks to the vault-server proxy over its Unix socket.
3. **No hot reload.** Adding a WASM tool/app requires `docker restart alf`. The existing CC `app_watcher.go` could be extended; not done.
4. **Go `.wasm` is fat** (~2.8 MB per guest, Go runtime overhead). TinyGo drops it to ~300 KB but loses stdlib features. Decide per tool.
5. **No cosign verification at load.** Deferred to Phase 7 of DELETIONS.md.
6. **Compile cache is in-memory only.** On daemon restart, first invocation re-compiles (~700 ms). Could persist to disk with a marshaled CompiledModule snapshot.

## Branch state

```
origin/spike/wasm   866df52
  fix(wasm): accept Bearer app token on /wasm-app/{slug}/*
  spike(wasm): mount app router in CC mux + CORS for null-origin iframes
  spike(wasm): deploy-wasm-playground.sh + sidebar-visible WASM app
  spike(wasm): cohost with legacy — tools live in data/tools, apps in data/apps
  fix(wasm): normalize hyphen → underscore for LLM schema
  spike(wasm): daemon integration + bundled tool + user-placed discovery
  spike(wasm): port runtime into main module + deletion roadmap
  spike(wasm): standalone WASM capabilities runtime POC
```

Local stash (recoverable):
```
stash@{0}: On release/0.8.0: wip release/0.8.0 (re-stashed to switch to spike/wasm)
```

## Resume tomorrow — decision tree

```
Did the spike validate? (yes — tool + app both work end-to-end)
  │
  ├─ (1) RFC + discussion              → no code, capitalize evidence
  │
  ├─ (2) Merge spike/wasm into main    → ~1h review, ships as feature-flagged
  │
  ├─ (3) Migrate revenue-scan (Tier 1) → ~3-4h, first real legacy replacement
  │
  ├─ (4) New capability: reports.write → ~2h, shows ABI extension pattern
  │
  └─ (5) Phase 3 of DELETIONS.md       → 1-2 weeks, start dismantling legacy
         (do not start without alignment on 1/2)
```
