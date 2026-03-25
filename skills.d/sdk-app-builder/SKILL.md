---
name: sdk-app-builder
description: Build standalone ALF apps — source-only (compiled at install), AlfSDK frontend, manifest, marketplace publishing
version: "5"
triggers: create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application, marketplace tool, app sdk, sdk app, new app with sdk, app with theme, interactive app
tier: sonnet
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained and installable via the marketplace.

**CRITICAL RULES:**
- **Source-only** — NEVER compile or generate binaries. ALF compiles Go at install time.
- **SQLite** for all data storage — no external databases.
- **Vanilla JS** for frontends — no frameworks, no build step, CSP-safe.
- **Standalone** — no dependency on shared databases or external processes.

## Scope check

Before building, if the request has fewer than 2 concrete details, ask:
- What should the app do? What actions?
- Does it need a web UI? What should it show?
- What data does it store?

## Pick the right architecture

| | CLI tool (appsdk) | REST server |
|---|---|---|
| **Best for** | Data tools the LLM calls | Rich web UIs, games, complex apps |
| **Backend** | Go `main.go` with appsdk | Go/Python with `service.json` |
| **Frontend** | `AlfSDK.tool()` | Direct fetch to server |
| **LLM tool** | Always | Only if LLM needs data access |
| **Example** | Journal, Todo, Bookmarks | 2048, Wordle, Dashboard |

**CLI tool**: LLM needs CRUD operations. May have optional web UI.
**REST server**: User-facing interactive app. No CLI tool unless LLM needs data access.
**Do NOT create a CLI tool** for games, calculators, visual tools.

## Reference docs (read before building)

Read the relevant reference file for templates, patterns, and API details:

| File | Read when |
|---|---|
| `reference/CLI-TOOL.md` | Building a CLI tool app (appsdk, manifest, go source) |
| `reference/REST-SERVER.md` | Building a REST server app (service.json, Go/Python server) |
| `reference/FRONTEND.md` | Any app with a web UI (AlfSDK init, theme, CSS vars, template) |
| `reference/GAMES.md` | Canvas games (resize, input, d-pad, overlay, HUD) |

## Common rules (all apps)

### Required files
- `manifest.json` — slug, version, description, category, icon, tools (if CLI)
- `app.json` — `{ "name": "...", "icon": "lucide-icon", "description": "..." }` (if web UI)
- `go.mod` — with all dependencies declared

### Data storage
- **SQLite only** (`modernc.org/sqlite` for Go, `sqlite3` for Python)
- Database in `data/<slug>.db`, WAL journal mode, `SetMaxOpenConns(1)`

### Frontend rules
- Init `AlfSDK` with `onThemeChange` — see `reference/FRONTEND.md`
- CSS variables only: `--bg`, `--text`, `--accent`, `--bg-card`, `--border`, `--text-dim`, `--on-accent`, `--radius`, `--green`, `--red`, `--yellow`, `--mauve`, `--sapphire`
- Set `font-family` explicitly (Google Fonts blocked by CSP in iframes)
- No `unsafe-eval` frameworks (Vue, Angular, Petite Vue)
- No external scripts/stylesheets (CSP blocks them)
- Lucide SVG icons only — inline from lucide.dev
- External APIs via `vault proxy <service> <method> <path>` — never hardcode keys

## Checklist

- [ ] Source-only — no compiled binaries
- [ ] Standalone — SQLite, no shared databases
- [ ] `manifest.json` with slug, version, description
- [ ] `app.json` with Lucide icon (if web UI)
- [ ] `index.html` with AlfSDK + theme CSS + explicit font-family
- [ ] `go.mod` with all dependencies
- [ ] CLI tool: `main.go` with appsdk + tool schema in manifest
- [ ] REST server: `service.json` + free port + `data/port`
