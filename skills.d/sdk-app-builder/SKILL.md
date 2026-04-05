---
name: sdk-app-builder
description: Build standalone ALF apps — source-only (compiled at install), AlfSDK frontend, manifest, marketplace publishing
version: "14"
triggers: app, sdk, create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application, marketplace tool, app sdk, sdk app, new app with sdk, app with theme, interactive app, todo app, application, develop app
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained and installable via the marketplace.

**CRITICAL RULES:**
- **Compatible = SDK + AIG** — an app is only "compatible" when it uses the AlfSDK correctly AND follows AIG design guidelines. Both are required, not optional.
- **Source-only for marketplace** — published apps ship as source code only (compiled at install time). For local development, compile and run directly.
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
| `reference/SKELETON.html` | **ALWAYS copy first** — complete starting template with theme, stat-grid, card-group, filters, CRUD, sheets. Adapt to your app. |
| `reference/AIG.md` | **ALWAYS read** — design rules, tokens, Do/Don't, zero custom CSS rule |
| `reference/AIG-COMPONENTS.md` | **ALWAYS read** — all `<alf-*>` web components with attrs, events, JS API |
| `reference/UI-UX.md` | **ALWAYS read** — UI/UX design principles: hierarchy, states, feedback, navigation, color, responsive |
| `reference/FRONTEND.md` | AlfSDK API details (storage, sheets, tool, confirm, haptics, events) |
| `reference/CLI-TOOL.md` | Building a CLI tool app (appsdk, manifest, go source) |
| `reference/REST-SERVER.md` | Building a REST server app (service.json, Go/Python server) |
| `reference/GAMES.md` | Canvas games (resize, input, d-pad, overlay, HUD) |
| `reference/SANDBOX.md` | Vault proxy, external APIs, sandbox constraints |

## Common rules (all apps)

### Required files
- `manifest.json` — slug, version, description, category, icon, tools (if CLI), permissions
- `app.json` — `{ "name": "...", "icon": "lucide-icon", "description": "..." }` (if web UI)
- `go.mod` — with all dependencies declared **(only if Go backend)**

### Data storage
- **Frontend-only apps**: use `AlfSDK.storage` (server-side key-value, persists across updates)
- **Go apps**: SQLite only (`modernc.org/sqlite`), database in `data/<slug>.db`, WAL mode, `SetMaxOpenConns(1)`

### Frontend rules
- **Use `<alf-*>` web components** — tabs, inputs, dialogs, lists, stats, alerts, etc. are ALL `<alf-*>` custom elements. Never compose raw CSS classes for patterns that have a component. Read `reference/AIG-COMPONENTS.md` for the full reference. `alf-ui.css` (auto-injected) provides styling; `alf-components.js` (auto-injected) provides the components.
- **Use AlfSDK v4 APIs** — audio, storage, confirm/prompt, haptics, clipboard, badges, viewport, events. See `reference/FRONTEND.md`.
- CSS variables only (no hardcoded colors), `--space-*` tokens, explicit `font-family`, Lucide SVG icons.
- **Lightweight eval-based frameworks OK** (Alpine.js, Petite Vue) — `unsafe-eval` is in CSP. No build-step frameworks (React, Vue SPA, Angular). No external scripts/stylesheets (CSP blocks them).
- No `localStorage`, `document.cookie`, or `credentials: 'same-origin'` — iframes are sandboxed. Use `AlfSDK.storage` and `AlfSDK.api()`.
- **Never use `fetch()` directly** — use `AlfSDK.api()` (returns parsed JSON, throws on non-2xx) or `AlfSDK.fetch()` (returns raw Response, for binary/streaming).
- External APIs via vault proxy — see `reference/SANDBOX.md`.

## Final checklist (MANDATORY — run before telling the user "it's ready")

**Compatibility (SDK + AIG — both required):**
- [ ] AlfSDK initialized with `onThemeChange(palette, isDark)` callback (2 args, not 1)
- [ ] `onThemeChange` updates both palette CSS and dark/light mode (`data-theme` or theme CSS)
- [ ] No absolute URLs to CC domain — use `/static/...` relative paths only
- [ ] All UI uses `<alf-*>` web components — no manual CSS class composition for standard patterns
- [ ] CSS variables only, `--space-*` tokens, no inline style overrides
- [ ] All 4 content states designed: loading, empty, populated, error (see UI-UX.md)
- [ ] Every action has user feedback: toast, haptics, or inline indicator

**All apps:**
- [ ] `manifest.json` with slug, version, description, permissions
- [ ] `app.json` with Lucide icon (if web UI)
- [ ] `index.html` with AlfSDK + theme CSS + explicit font-family
- [ ] Run: `ls -la ~/data/apps/<slug>/manifest.json ~/data/apps/<slug>/app.json`
- [ ] Run: `cat ~/data/apps/<slug>/manifest.json | grep permissions`

**Frontend-only** — `AlfSDK.storage`, `"permissions": ["storage"]`, no `.go` files:
- [ ] Run: `ls ~/data/apps/<slug>/*.go 2>/dev/null && echo "ERROR: Go files in frontend-only app"`

**CLI tool** — `go.mod`, `main.go` with appsdk, `"permissions": ["bash", "storage"]`

**REST server** — `service.json` + free port + `data/port`, vault via `appsdk.NewVaultClient()`

**NEVER say "it's ready" without running these checks.** Fix any failure before reporting success.
