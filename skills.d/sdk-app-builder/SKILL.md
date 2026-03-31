---
name: sdk-app-builder
description: Build standalone ALF apps — source-only (compiled at install), AlfSDK frontend, manifest, marketplace publishing
version: "13"
triggers: app, sdk, create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application, marketplace tool, app sdk, sdk app, new app with sdk, app with theme, interactive app, todo app, application, develop app
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained and installable via the marketplace.

**CRITICAL RULES:**
- **Source-only** — NEVER compile or generate binaries. ALF compiles Go at install time.
- **SQLite** for all data storage — no external databases.
- **Vanilla JS** for frontends — no frameworks, no build step, CSP-safe.
- **Standalone** — no dependency on shared databases or external processes.
- **Sandboxed** — all code runs in a chroot jail. No access to vault, secrets, or other apps' data.

## Scope check

Before building, if the request has fewer than 2 concrete details, ask:
- What should the app do? What actions?
- Does it need a web UI? What should it show?
- What data does it store?

## Pick the right architecture

| | Frontend-only | CLI tool (appsdk) | REST server |
|---|---|---|---|
| **Best for** | Simple user apps | Data tools the LLM calls | Complex apps with heavy backend |
| **Backend** | None — `AlfSDK.storage` | Go `main.go` with appsdk | Go/Python with `service.json` |
| **Frontend** | `index.html` + vanilla JS | `AlfSDK.tool()` | Direct fetch to server |
| **LLM tool** | No | Always | Only if LLM needs data access |
| **Compilation** | None — works instantly | Go compile at install | Go/Python at install |
| **Example** | Todo, Notes, Timer, Tracker | Journal, Bookmarks | 2048, Dashboard, API proxy |

**Decision tree — follow in order:**

1. **Does the LLM need to call this app as a tool via bash?** (e.g., "add a journal entry", "search bookmarks")
   → Yes: **CLI tool** (Go + appsdk + manifest tools)
   → No: continue ↓

2. **Does the app need server-side logic that JS can't do?** (SQLite queries, external API calls via vault, file processing, cron jobs, WebSocket server)
   → Yes: **REST server** (Go/Python + service.json)
   → No: continue ↓

3. **Everything else → Frontend-only** (index.html + AlfSDK.storage + vanilla JS)
   This covers: todo lists, notes, trackers, timers, calculators, games, dashboards with local data, habit trackers, budget planners, etc.

**Frontend-only is the default.** No Go, no compilation, instant install. `AlfSDK.storage` provides persistent server-side key-value storage — sufficient for most apps.

**Do NOT create a CLI tool** for games, calculators, visual tools.
**Do NOT use Go** when `AlfSDK.storage` can handle the data needs.

## Reference docs (read before building)

Read the relevant reference file for templates, patterns, and API details:

| File | Read when |
|---|---|
| `reference/AIG.md` | **ALWAYS read first** — design system, components, spacing, colors, patterns |
| `reference/CLI-TOOL.md` | Building a CLI tool app (appsdk, manifest, go source) |
| `reference/REST-SERVER.md` | Building a REST server app (service.json, Go/Python server) |
| `reference/FRONTEND.md` | Any app with a web UI (AlfSDK init, theme, CSS vars, template) |
| `reference/GAMES.md` | Canvas games (resize, input, d-pad, overlay, HUD) |

## Common rules (all apps)

### Required files
- `manifest.json` — slug, version, description, category, icon, tools (if CLI), permissions
- `app.json` — `{ "name": "...", "icon": "lucide-icon", "description": "..." }` (if web UI)
- `go.mod` — with all dependencies declared **(only if Go backend)**

### Data storage
- **Frontend-only apps**: use `AlfSDK.storage` (server-side key-value, persists across updates)
- **Go apps**: SQLite only (`modernc.org/sqlite`), database in `data/<slug>.db`, WAL mode, `SetMaxOpenConns(1)`

### Frontend rules
- **Follow AIG** — use `alf-ui.css` classes (auto-injected into iframes). See `reference/AIG.md`.
- **`alf-ui.css` works on ALL elements** — including those created dynamically via `innerHTML`, `createElement`, or any JS rendering. It is a `<style>` tag in the iframe, not scoped. NEVER duplicate or re-implement AIG classes — just use them.
- **Use `.btn`, `.card`, `.input`, `.form-group`** etc. — never write inline styles or custom classes for standard components.
- Init `AlfSDK` with `onThemeChange` — see `reference/FRONTEND.md`
- **Use SDK v2 APIs** — audio, storage, confirm/prompt, haptics, clipboard, badges, viewport, events
- **Audio: always use `AlfSDK.audio`** — never create your own AudioContext (mobile unlock handled by SDK)
- **Storage: use `AlfSDK.storage`** for persistent data — server-side key/value, survives app updates
- **Dialogs: use `AlfSDK.confirm()` / `AlfSDK.prompt()`** — renders as iOS bottom sheet on mobile
- CSS variables only: `--bg`, `--text`, `--accent`, `--bg-card`, `--bg-input`, `--border`, `--text-dim`, `--on-accent`, `--radius`, `--green`, `--red`, `--yellow`, `--mauve`, `--sapphire`, `--pink`, `--teal`, `--peach`, `--lavender`, `--danger`, `--success`
- Spacing tokens: `--space-xs` (4px), `--space-sm` (8px), `--space-md` (16px), `--space-lg` (24px), `--space-xl` (32px)
- Set `font-family` explicitly (Google Fonts blocked by CSP in iframes)
- No `unsafe-eval` frameworks (Vue, Angular, Petite Vue)
- No external scripts/stylesheets (CSP blocks them)
- Lucide SVG icons only — inline from lucide.dev
- External APIs via vault proxy — never hardcode keys

## Vault proxy (external API access)

Apps that need external API access (OpenRouter, Google APIs, etc.) declare services in `manifest.json`:
```json
{
  "services": ["openrouter", "google-api"]
}
```

The daemon creates a per-app vault proxy socket (`VAULT_PROXY_SOCK` env var) that only allows the declared services. The proxy injects authentication server-side — apps never see API keys or vault tokens.

**Go server apps** use `pkg/appsdk`:
```go
vc, _ := appsdk.NewVaultClient()
resp, err := vc.Proxy("openrouter", "POST", "/v1/chat/completions", body)
```

Or via the `App` helper:
```go
app := appsdk.New("my-app")
app.Vault().ProxyJSON("openrouter", "POST", "/v1/chat/completions", req, &resp)
```

## Sandbox constraints

All app code (both `AlfSDK.bash()` and backend servers) runs inside a chroot jail:

- **DO** store data in the app's own `data/` directory
- **DO** use `fetch()` to own REST proxy endpoints (`/apps/{slug}/api/...`)
- **DO** declare permissions in `manifest.json`
- **DO** declare vault `services` in `manifest.json` for external API access
- **DO** use `appsdk.NewVaultClient()` or `VAULT_PROXY_SOCK` for vault access
- **DO NOT** access `/home/alf/data/apps/other-app/` — other apps' directories are invisible
- **DO NOT** hardcode API keys or rely on `VAULT_TOKEN` — use the vault proxy

Apps needing data beyond their own directory should use the **REST proxy pattern**: the Go server exposes endpoints, the frontend fetches them, and the server accesses data within its sandbox only.

## Checklist

**All apps:**
- [ ] `manifest.json` with slug, version, description, permissions
- [ ] `app.json` with Lucide icon (if web UI)
- [ ] `index.html` with AlfSDK + theme CSS + explicit font-family + `alf-ui.css` classes
- [ ] No access to other apps' directories or system paths

**Frontend-only apps (preferred for simple apps):**
- [ ] Data stored via `AlfSDK.storage` (persistent key-value)
- [ ] `"permissions": ["storage"]` in manifest.json
- [ ] No `go.mod`, no `main.go` — just HTML/JS

**CLI tool apps:**
- [ ] `go.mod` with all dependencies
- [ ] `main.go` with appsdk + tool schema in manifest
- [ ] `"permissions": ["bash", "storage"]` in manifest.json

**REST server apps:**
- [ ] `service.json` + free port + `data/port`
- [ ] Vault access via `appsdk.NewVaultClient()` (not direct HTTP to vault)

## Validation (MANDATORY before telling the user "it's ready")

After writing all files, you MUST verify the app works:

1. **Check files exist:**
   ```bash
   ls -la ~/data/apps/<slug>/index.html ~/data/apps/<slug>/app.json ~/data/apps/<slug>/manifest.json
   ```

2. **Validate manifest.json has required permissions:**
   ```bash
   cat ~/data/apps/<slug>/manifest.json | grep permissions
   ```
   - Frontend-only with storage → must contain `"storage"`
   - CLI tool → must contain `"bash"` and `"storage"`

3. **Validate app.json format:**
   ```bash
   cat ~/data/apps/<slug>/app.json
   ```
   Must have `name`, `icon` (Lucide icon name), `description`.

4. **For frontend-only apps, verify NO Go files exist:**
   ```bash
   ls ~/data/apps/<slug>/*.go 2>/dev/null && echo "ERROR: Go files found in frontend-only app"
   ```

5. **Open the app** — tell the user to refresh the CC page and click the app.

**NEVER say "it's ready" without running these checks.** If any check fails, fix it before reporting success.
