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

## Architecture mental model (read first)

Your app runs in **two different execution contexts**. Get this wrong and you'll chase phantom bugs for hours.

```
┌────────────────────────────── PARENT FRAME (Control Center) ──────────────────────────────┐
│  Full auth (session cookie). Can reach any CC URL directly.                                │
│  Sheets you open with AlfSDK.sheet(html, actions) RENDER HERE — HTML runs in parent scope. │
│                                                                                             │
│  ┌─────────────────────── IFRAME (your app) ────────────────────────┐                      │
│  │  sandbox="allow-scripts allow-forms allow-popups ..." (no allow-same-origin)              │
│  │  → Origin: null (opaque). No cookies, no top.*, no parent.document.                       │
│  │  AlfSDK obtains a short-lived Bearer token via MessageChannel on init.                    │
│  │                                                                                            │
│  │  AlfSDK.api(path)    → fetch(path, {Authorization: Bearer ...})  ← token attached         │
│  │  AlfSDK.fetch(path)  → same, raw Response (for blobs/streams)                              │
│  │  AlfSDK.sheet(html)  → postMessage to parent, parent renders the HTML                      │
│  │                                                                                            │
│  │  <img src="/apps/SLUG/foo.png">        ✅ static file in your app dir (auth bypass        │
│  │                                           via sandbox sub-resource gate)                    │
│  │  <img src="/apps/SLUG/api/42.jpg">     ✅ proxied to your backend (asset ext bypass)       │
│  │  <img src="/apps/SLUG/api/data.json">  ❌ data endpoint → 401. Use AlfSDK.api() instead.   │
│  │  fetch('/api/vault/...') without SDK    ❌ no Bearer → 401                                  │
│  └──────────────────────────────────────────────────────────────────┘                      │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

**Consequences:**

1. **Two HTTP worlds** — the iframe needs `AlfSDK.api()`/`AlfSDK.fetch()` for every authenticated call because the browser can't attach the Bearer token on its own. Raw `fetch()` in an iframe → 401.
2. **One exception**: browser tag loads (`<img>`, `<audio>`, `<video>`, `@font-face`) can use direct URLs if the path ends with a media/font extension — the CC recognizes sandboxed sub-resources via `Sec-Fetch-*` + `Origin: null` and waives auth. **Only** for `.png .jpg .jpeg .gif .webp .svg .ico .avif .woff .woff2 .ttf .otf .eot .mp3 .mp4 .webm .ogg .wav` under `/apps/{slug}/` OR `/apps/{slug}/api/...`. Everything else (including `.json`, `.js`, `.css`, `.wasm`) requires the SDK.
3. **Sheets render in the parent frame** — `AlfSDK.sheet(html)` sends HTML to the Control Center for rendering. The HTML runs *outside* your iframe with full CC origin, so direct `<img src="/apps/SLUG/api/42.jpg">` works there even without the sub-resource bypass. Don't mix the two contexts up: code in `AlfSDK.sheet()` is NOT in your iframe.
4. **REST server apps**: your backend listens on `127.0.0.1:{port}` (port written to `data/port`) and the CC reverse-proxies `/apps/{slug}/api/*` → `localhost:{port}/api/*`. Strip `Cookie`, `Authorization`, and forwarded headers — app servers are untrusted and the CC removes them before forwarding.
5. **Cross-app calls** go through `AlfSDK.action(targetSlug, action, params)` — declared in the target's `manifest.json`. Never call another app's `/apps/{other}/api/*` directly; it's blocked by referer check (SEC-005).

If a hypothesis about routing or auth doesn't match one of these 5 points, the hypothesis is wrong — re-check the context (iframe vs parent) before digging.

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
- `README.md` — **mandatory for every app**. 10–30 lines, human-readable. Must include:
  1. **Architecture** in one sentence (frontend-only / CLI tool / REST server + storage choice).
  2. **Data layout** — what's in `data/` (files, DB name, directories).
  3. **API routes** — list every `/api/...` route the backend exposes (if any), with method + purpose.
  4. **Permissions** — what the app needs (`storage`, `bash`, `network`, etc.) and why.
  5. **Quirks** — anything non-obvious the next developer (or LLM) should know before touching the code.

  The README is the first thing another LLM reads when asked to fix a bug — without it, they'll rebuild the mental model by grepping the code, wasting tokens and making wrong guesses.

### Data storage
- **Frontend-only apps**: use `AlfSDK.storage` (server-side key-value, persists across updates). On-disk: `data/apps/{slug}/data/storage.json` — readable directly via `cat`
- **Go apps**: SQLite only (`modernc.org/sqlite`), database in `data/<slug>.db`, WAL mode, `SetMaxOpenConns(1)`

### Frontend rules
- **Use `<alf-*>` web components** — tabs, inputs, dialogs, lists, stats, alerts, etc. are ALL `<alf-*>` custom elements. Never compose raw CSS classes for patterns that have a component. Read `reference/AIG-COMPONENTS.md` for the full reference. `alf-ui.css` (auto-injected) provides styling; `alf-components.js` (auto-injected) provides the components.
- **Use AlfSDK v4 APIs** — audio, storage, confirm/prompt, haptics, clipboard, badges, viewport, events. See `reference/FRONTEND.md`.
- CSS variables only (no hardcoded colors), `--space-*` tokens, explicit `font-family`, Lucide SVG icons.
- **Lightweight eval-based frameworks OK** (Alpine.js, Petite Vue) — `unsafe-eval` is in CSP. No build-step frameworks (React, Vue SPA, Angular). No external scripts/stylesheets (CSP blocks them).
- No `localStorage`, `document.cookie`, or `credentials: 'same-origin'` — iframes are sandboxed. Use `AlfSDK.storage` and `AlfSDK.api()`.
- **Never use `fetch()` directly for data** — use `AlfSDK.api()` (parsed JSON, throws on non-2xx) or `AlfSDK.fetch()` (raw Response, for blobs/streaming). Both attach the Bearer token automatically.
- **`<img>`, `<audio>`, `<video>`, `@font-face` CAN use direct URLs** — if the asset is served under `/apps/{slug}/` or `/apps/{slug}/api/...` with a media/font extension (`.png .jpg .webp .svg .woff2 .mp4 ...`). See the "Architecture mental model" section. Data endpoints still require `AlfSDK.api()`.
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

## E2E verification (MANDATORY — run on every creation AND modification)

After the checklist above passes, run an end-to-end test to confirm the app actually works:

**Frontend-only apps:**
1. Run: `app enable <slug>` (if not already enabled)
2. Run: `curl -s http://localhost:${CC_PORT:-9400}/apps/<slug>/` → must return 200 with HTML containing `AlfSDK`
3. If the app uses `AlfSDK.storage`, test a write+read cycle:
   ```bash
   curl -s -X PUT http://localhost:${CC_PORT:-9400}/api/apps/<slug>/storage/test -d '{"value":"e2e"}' 
   curl -s http://localhost:${CC_PORT:-9400}/api/apps/<slug>/storage/test
   # Cleanup:
   curl -s -X DELETE http://localhost:${CC_PORT:-9400}/api/apps/<slug>/storage/test
   ```

**CLI tool apps:**
1. Run: `app enable <slug>` (if not already enabled)
2. Run the tool with `--help` → must exit 0
3. Run the tool with a **real test case** that exercises the primary action
4. Verify exit code 0 and expected output
5. Add `x-test` to the tool's JSON schema (same format as tool-creator)

**REST server apps:**
1. Run: `app enable <slug>` → wait 2s for server startup
2. Run: `curl -s http://localhost:$(cat ~/data/apps/<slug>/data/port)/health` → must return 200
3. Test one real endpoint with sample data

**If any E2E step fails, fix the issue immediately.** Do NOT deliver the app until E2E passes.

**NEVER say "it's ready" without running these checks AND E2E verification.** Fix any failure before reporting success.
