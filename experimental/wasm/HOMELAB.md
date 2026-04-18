# Deploy & test the WASM runtime on your homelab

> Last updated end of day 2026-04-19 (commit `866df52`). Companion docs:
> [INTEGRATION.md](INTEGRATION.md) (full architecture), [DELETIONS.md](DELETIONS.md), [SPIKE.md](SPIKE.md).

Scope: after deploying `spike/wasm` to the homelab, verify the bundled
WASM tool is live, drop in user-placed tools/apps via SSH, and see them
work same-origin in the CC sidebar — **no tunnel required**.

## What's already in the binary after `dev-deploy.sh`

- `internal/runtime/wasm` — wazero-backed runtime with compile cache
- 1 **bundled tool** (`wasm-demo`) embedded via `go:embed` — registered
  automatically at boot, usable by any LLM backend that queries
  ALF's tool registry
- **Discovery** scans on startup:
  - `/home/alf/data/wasm-bundled/*/manifest.toml` (extracted from binary)
  - `/home/alf/data/tools/*/manifest.toml` (user-placed)
  - `/home/alf/data/apps/*/manifest.toml` (user-placed)
- **App router mounted inside the CC mux** at `/wasm-app/<slug>/*`.
  Iframe fetches via `AlfSDK.fetch` are same-origin and flow through
  the full CC middleware stack (auth, CORS, rate limit, CSRF,
  security headers).

Legacy sandbox, integrity guard, subprocess tools, marketplace apps are
**untouched**. Zero regression.

---

## Verify after deploy

```bash
ssh alessandro@192.168.129.101 "docker logs --tail=60 alf 2>&1 | grep -iE 'wasm'"
```

Expected:

```
[wasm] registered tool "wasm-demo" (from /home/alf/data/wasm-bundled/tool-demo/manifest.toml)
[wasm-app] registered "wasm-playground" (frontend=false)     # if playground deployed
[wasm] discovery: 1 tool(s), N app(s) registered
tooling: loaded 27 tool schemas: [… wasm_demo …]
```

Test the tool from any LLM backend that sees ALF's registry (OpenRouter,
Anthropic API). Ask Claude:

> *use the wasm-demo tool with input "bonjour"*

Daemon logs:

```
tooling: executing tool wasm_demo args={"input": "bonjour"}
[wasm:wasm-demo] info: wasm-demo invoked
[wasm:wasm-demo] info: wasm-demo done (run N)
toolloop: tool wasm_demo → 27 chars (error=false)
```

Response: `{"echo":"bonjour","runs":N}`.

> **Note on Codex**: the Codex CLI discovers tools via filesystem scan
> (`$PATH` + `toolbox.md`), not via ALF's in-memory registry. WASM tools
> are invisible to Codex until an MCP server exposes them — out of scope
> for this spike.

---

## Deploy the WASM playground app (full demo)

```bash
bash scripts/deploy-wasm-playground.sh
```

Places 4 files under `/home/alf/data/apps/wasm-playground/`:

| File | Role |
|---|---|
| `manifest.json` | Marketplace format — sidebar entry (category=developer, icon=code) |
| `manifest.toml` | WASM runtime format — registers the app (kind=app, permissions) |
| `index.html` | Iframe content — uses `AlfSDK.init` + `AlfSDK.fetch` for auth |
| `wasm-playground.wasm` | Compiled guest (Go → wasip1, ~3 MB) |

After the restart, open the CC → sidebar → **WASM Playground** under
*developer*. The iframe loads; four buttons call the WASM backend:

| Button | Expected response |
|---|---|
| `/api/hello` | `{"message":"hello from a sandboxed WASM app","method":"GET","runtime":"go-wasip1","sandbox":"wazero + manifest-gated host imports"}` |
| `/api/counter` (clicked 3×) | `{"requests_served":1}` → 2 → 3 (storage KV persists per-capability) |
| `/api/btc` | JSON from coingecko (allowed vault service) |
| `/api/denied-demo` | `{"error":"vault.request: permission denied (manifest did not grant it)","expected":"denied"}` — proves the Policy gate |

Live log tail:

```bash
ssh alessandro@192.168.129.101 "docker logs -f alf 2>&1 | grep wasm"
```

Per click you should see:

```
[wasm:wasm-playground] info: app-hello handling GET /api/hello
[wasm-app] GET /wasm-app/wasm-playground/api/hello -> 200 (XXms)
```

---

## Placing other user-provided capabilities

### A new WASM tool

1. Build locally: `GOOS=wasip1 GOARCH=wasm go build -o mytool.wasm .`
2. Write a `manifest.toml` (`kind = "tool"`, declared permissions only)
3. Copy both into `/home/alf/data/tools/<slug>/` on the container
4. `docker restart alf`
5. The tool appears in the next `tooling: loaded … schemas` log line and
   is immediately callable by any API-backed LLM.

### A new WASM app (sidebar-visible)

Follow the 4-file pattern used by `deploy-wasm-playground.sh`:
`manifest.json` + `manifest.toml` + `index.html` + `.wasm`, placed under
`/home/alf/data/apps/<slug>/`. Restart. Click the sidebar entry.

The `index.html` MUST:
- Include `<script src="/static/alf-app-sdk.js"></script>`
- Call `AlfSDK.init({ slug: '<your-slug>' })`
- Use `AlfSDK.fetch('/wasm-app/<your-slug>/...')` — raw `fetch()` will 401
  (iframe has `Origin: null`, no cookies, needs Bearer app token)

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `wasm runtime init failed` at boot | `data/wasm-data` not creatable | Check directory permissions for uid 1001 (alfd) |
| Guest fails with `unresolved import` | Guest called a host fn not in manifest | Add the permission, or remove the call |
| LLM reports tool missing | Wrong name (hyphens vs underscores) | The adapter auto-normalizes `-` → `_`; tool_call names come through OK |
| App iframe `401 Unauthorized` | `index.html` uses raw `fetch()` | Switch to `AlfSDK.fetch()` — it attaches the Bearer app token |
| App iframe CORS error | Path not `/wasm-app/<slug>/*` | That's the only mount point accepted for null-origin — update the URL |
| Buttons work via curl+bearer but not iframe | Browser cache of old `index.html` | Cmd+Shift+R to force-reload |
| `/api/btc` returns rc=-5 | Default vault client hits public coingecko — outbound may be firewalled | Known limitation — wire a real VaultClient backed by your vault-proxy |

---

## What's intentionally still NOT integrated

- **Cosign signature verification** at module load
- **Hot reload** (daemon restart still required)
- **MCP server** exposing WASM tools to Codex CLI
- **nsjail fallback** for Classe C binaries (ffmpeg, whisper, claude CLI)
- **TinyGo build target** for smaller guest artifacts

See [DELETIONS.md](DELETIONS.md) for the phased roadmap and
[INTEGRATION.md](INTEGRATION.md) for the current architecture.
