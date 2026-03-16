---
name: app-builder
description: Create or align a web app with the ALF standard framework — SQLite + Go alf-api + /api/bash bridge + theme.css
version: "2"
triggers: create app, new app, build app, make app, web app, align app, migrate app, webapp, build application, create application
---

# ALF App Framework — Standard Directives

## Before building — MANDATORY scope check

**NEVER start building unless the user has described at least one concrete feature or data field.**

A request like "create a habit tracker app" or "build me a crypto app" is NOT enough. These are just app names — you have no idea what the user actually wants.

**Rule: if the request contains fewer than 2 concrete details (specific features, data fields, behaviors, or API sources), you MUST ask questions first.** Do not assume or invent features.

Ask 2-3 short, targeted questions:
- What are the core features? (e.g. "track daily habits with streaks" vs "habit templates with categories and reminders")
- What data does it work with? (user input only? external API? scheduled feed?)
- Any must-haves? (specific columns, calculations, integrations)

**Examples that require questions first:**
- "create a crypto app" → ask: portfolio tracker? price alerts? trading journal? which coins/APIs?
- "build a habit tracker" → ask: what do you track? streaks? categories? reminders?
- "make a fitness app" → ask: workout logging? meal tracking? progress charts?

**Examples specific enough to build directly:**
- "create a reading list with title, author, status, rating columns and a form to add books"
- "build a crypto portfolio tracker showing holdings, current price from CoinGecko, and P&L"

**When the user references an existing app** (by name or slug), work on that app — don't create a new one. Check `~/data/apps/` for existing app directories.

---

When creating or modifying a web app in ALF, follow this framework **exactly**. No exceptions.

---

## Architecture (mandatory for ALL apps)

```
┌─────────────┐  POST /api/bash   ┌──────────┐  curl localhost:8764  ┌──────────┐  SQLite  ┌──────────────┐
│  index.html │ ────────────────► │  daemon   │ ──────────────────► │  alf-api │ ◄──────► │  <app>.db    │
│  (browser)  │ ◄──────────────── │  (CC)     │ ◄────────────────── │  (Go)    │          │  apps/<app>/ │
└─────────────┘      JSON         └──────────┘       JSON           └──────────┘          └──────────────┘
                                                                          ▲
                                                                          │ writes
                                                                   ┌──────┴───────┐
                                                                   │ Scheduled jobs│
                                                                   │ (cron/script) │
                                                                   └──────────────┘
```

### How it works

1. **Frontend** (`index.html`) calls `POST /api/bash` with a curl command
2. **Daemon** (Control Center) authenticates via session cookie, executes the command
3. **Command** is `curl -s http://127.0.0.1:8764/api/<app>` targeting the Go API server
4. **alf-api** (Go, port 8764, localhost only) queries SQLite and returns JSON
5. **Frontend** parses the JSON response and renders the UI

**One Go API server. One data pattern. `/api/bash` as the bridge. No exceptions.**

---

## Rules

### R1 — Data storage is ALWAYS SQLite
- Every app that has data MUST use a SQLite database at `apps/<app-name>/data/<app-name>.db`
- Use WAL journal mode: `PRAGMA journal_mode=WAL`
- Never bake data into HTML as JS constants
- Never use loose JSON files as primary data store
- Schema is auto-created in the Go `register<App>()` function

### R2 — All data access goes through alf-api (Go)
- The single Go API server at `/home/alf/data/tools/api-server/alf-api` (port 8764, localhost only) handles ALL app data
- Each app registers handlers in a `<app>.go` file and is wired in `main.go`
- Route pattern: `/api/<app-name>` for GET, POST, PATCH, DELETE
- **Never** create a separate API server per app
- **Never** use `/api/workspace` to read JSON files

### R3 — Frontend calls alf-api via `/api/bash` bridge
- The daemon (Control Center) does NOT proxy `/api/*` to alf-api
- The ONLY way for the frontend to reach alf-api is through `/api/bash`
- Use this standard helper function in every app:

```javascript
async function api(method, path, body, silent = false) {
  try {
    let cmd = `curl -s -X ${method} http://127.0.0.1:8764${path}`;
    cmd += " -H 'Content-Type: application/json'";
    if (body !== undefined) cmd += ` -d '${JSON.stringify(body).replace(/'/g, "'\\''")}'`;
    const res = await fetch('/api/bash', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command: cmd })
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    if (data.error) throw new Error(data.error);
    return JSON.parse(data.output);
  } catch (e) {
    if (!silent) console.error(e);
    return null;
  }
}
```

- **Never** use `fetch('/api/<app>')` directly — the daemon will return 401
- **Never** point to an external port — alf-api listens on 127.0.0.1 only

### R4 — Scheduled jobs write to the DB, never to HTML
- Cron jobs and scheduled tasks MUST write results to the app's SQLite DB
- The job script connects to `apps/<app-name>/data/<app-name>.db` and inserts/updates rows
- The frontend reads fresh data via alf-api on next page load
- Never regenerate or rewrite index.html to update data

### R5 — Theming via CSS variables (no inline theme blocks)
- Link the shared stylesheet with palette support (two tags required, in this order):
  ```html
  <link rel="stylesheet" id="alf-theme-link" href="/static/theme-sage.css">
  <script src="/static/theme-init.js"></script>
  ```
  `theme-init.js` reads `localStorage('alf-palette')` and swaps the href automatically.
- Use ONLY these CSS variables for theming:
  - `var(--bg)` — page background
  - `var(--text)` — main text
  - `var(--text-dim)` — secondary text
  - `var(--accent)` — brand color, links, highlights
  - `var(--border)` — borders, dividers
  - `var(--bg-card)` — card/panel background
  - `var(--on-accent)` — text on accent backgrounds
  - `var(--green)` — success, positive
  - `var(--red)` — error, danger
  - `var(--yellow)` — warning
  - `var(--radius)` — border radius
- App-specific styles go in a `<style>` block AFTER the theme link
- **Never** hardcode colors — always use CSS variables
- **Never** inline the theme.css content into the HTML

### R6 — Standard app structure
```
apps/<app-name>/
├── app.json              # REQUIRED — metadata
├── index.html            # REQUIRED — single-file frontend
├── service.json          # OPTIONAL — background service (auto-supervised by daemon)
└── data/
    └── <app-name>.db     # SQLite database (created by alf-api or job script)
```

### R7 — app.json format
```json
{
  "name": "Human-Readable Name",
  "icon": "lucide-icon-name",
  "description": "One-line description of what this app does"
}
```
- Icon must be a valid Lucide icon name (kebab-case)

### R8 — Background services (service.json)

If alf-api or any app needs a persistent background process, declare it in `service.json`:

```json
{
  "name": "ALF API",
  "command": "./alf-api",
  "args": ["--port", "8764"],
  "env": {"PORT": "8764"},
  "restart": "always",
  "restart_delay": "3s",
  "max_restarts": 100,
  "enabled": true
}
```

| Field | Description | Default |
|-------|-------------|---------|
| `command` | Executable (relative to app dir, must stay within the app directory — absolute paths and `../` are rejected) | Required |
| `args` | Command-line arguments | `[]` |
| `env` | Environment variables (PATH, HOME, LD_* are blocked for security) | `{}` |
| `restart` | `"always"`, `"on-failure"`, or `"no"` | `"always"` |
| `restart_delay` | Base delay between restarts (exponential backoff up to 60s) | `"3s"` |
| `max_restarts` | Max restart attempts before giving up | `100` |
| `enabled` | Whether to auto-start | `true` |

The daemon supervises services automatically: start on boot, restart on crash with backoff, SIGTERM on shutdown. Services run under the same user as the daemon. Logs appear in daemon log prefixed with `[app-slug]`.

**Do NOT use `nohup ... &` or `bootstrap.sh` — use `service.json`.**

### R9 — Security constraints (enforced by CSP)
- No external scripts (`<script src="https://...">` blocked)
- No inline event handlers (`onclick="..."` blocked) — use `addEventListener()`
- No external stylesheets except `/static/theme-*.css` and `/static/theme-init.js`
- Fetch/XHR restricted to same origin (`/api/*` only — this is why we use `/api/bash`)
- Maximum file size: 5 MB
- File names: alphanumeric + hyphens only

### R10 — HTML template
Every new app MUST start from this template:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>APP_NAME</title>
  <link rel="stylesheet" id="alf-theme-link" href="/static/theme-sage.css">
  <script src="/static/theme-init.js"></script>
  <style>
    /* App-specific styles only — never redefine theme variables */
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; padding: 24px; background: var(--bg); color: var(--text); }
    .card { background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px; padding: 16px; margin-bottom: 12px; }
    h1 { color: var(--accent); margin-top: 0; }
    .loading { text-align: center; color: var(--text-dim); padding: 40px; }
    .error { text-align: center; color: #e74c3c; padding: 40px; }
  </style>
</head>
<body>
  <h1>APP_NAME</h1>
  <div id="app"><div class="loading">Loading…</div></div>

  <script>
    /* ── API helper — all calls go through /api/bash → curl → alf-api ── */
    async function api(method, path, body, silent = false) {
      try {
        let cmd = `curl -s -X ${method} http://127.0.0.1:8764${path}`;
        cmd += " -H 'Content-Type: application/json'";
        if (body !== undefined) cmd += ` -d '${JSON.stringify(body).replace(/'/g, "'\\''")}'`;
        const res = await fetch('/api/bash', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ command: cmd })
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        if (data.error) throw new Error(data.error);
        return JSON.parse(data.output);
      } catch (e) {
        if (!silent) console.error(e);
        return null;
      }
    }

    async function loadData() {
      const data = await api('GET', '/api/APP_SLUG', undefined, true);
      if (!data) {
        document.getElementById('app').innerHTML =
          '<div class="error">Failed to load data. Check that alf-api is running.</div>';
        return;
      }
      render(data);
    }

    function render(data) {
      const app = document.getElementById('app');
      // Build your UI here using data from the API
      app.innerHTML = '<p>Replace this with your app UI</p>';
    }

    loadData();
  </script>
</body>
</html>
```

---

## How to add a new app to alf-api (Go)

### 1. Create the Go handler file

Create `/home/alf/data/tools/api-server/<app>.go`:

```go
package main

import (
    "database/sql"
    "net/http"
    "os"
    "path/filepath"

    "github.com/go-chi/chi/v5"
)

const myappDB = appsDir + "/<app-name>/data/<app-name>.db"

type myappHandler struct {
    db *sql.DB
}

func registerMyapp(r chi.Router) error {
    os.MkdirAll(filepath.Dir(myappDB), 0755)

    db, err := openDB(myappDB)
    if err != nil {
        return err
    }

    // Auto-create schema
    if _, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS items (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT NOT NULL,
            data TEXT,
            created_at TEXT DEFAULT (datetime('now')),
            updated_at TEXT DEFAULT (datetime('now'))
        )
    `); err != nil {
        return err
    }

    h := &myappHandler{db: db}

    r.Get("/api/<app-name>", h.list)
    // Add POST, PATCH, DELETE as needed

    return nil
}

func (h *myappHandler) list(w http.ResponseWriter, r *http.Request) {
    rows, err := h.db.QueryContext(r.Context(),
        "SELECT id, title, data, created_at, updated_at FROM items ORDER BY id DESC")
    if err != nil {
        writeErr(w, 500, err.Error())
        return
    }
    defer rows.Close()

    items := []map[string]any{}
    for rows.Next() {
        var id int64
        var title, data, created, updated string
        if err := rows.Scan(&id, &title, &data, &created, &updated); err != nil {
            writeErr(w, 500, err.Error())
            return
        }
        items = append(items, map[string]any{
            "id": id, "title": title, "data": data,
            "created_at": created, "updated_at": updated,
        })
    }
    writeJSON(w, 200, items)
}
```

### 2. Register in main.go

Add to the `main()` function in `/home/alf/data/tools/api-server/main.go`:

```go
if err := registerMyapp(r); err != nil {
    log.Fatalf("myapp: %v", err)
}
```

### 3. Rebuild

**Always run this after creating or modifying a Go handler:**

```bash
export PATH="/home/alf/data/tools/go-sdk/bin:$PATH" && export GOPATH="/home/alf/data/tools/go-path" && cd /home/alf/data/tools/api-server && CGO_ENABLED=0 go build -o alf-api .
```

The daemon's service supervisor will detect the process exit and restart with the new binary automatically (if `service.json` is configured with `"restart": "always"`).

Verify the build includes your routes:
```bash
strings /home/alf/data/tools/api-server/alf-api | grep "/api/<app-name>"
```

---

## How to create a scheduled job that feeds data

Create `apps/<app-name>/jobs/refresh.py`:

```python
#!/usr/bin/env python3
"""Scheduled job — fetches data and writes to <app-name>.db"""
import os
import sqlite3

DB = "/home/alf/data/apps/<app-name>/data/<app-name>.db"

def ensure_schema(conn):
    conn.execute("""
        CREATE TABLE IF NOT EXISTS items (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT NOT NULL,
            data TEXT,
            created_at TEXT DEFAULT (datetime('now')),
            updated_at TEXT DEFAULT (datetime('now'))
        )
    """)
    conn.execute("PRAGMA journal_mode=WAL")

def refresh():
    os.makedirs(os.path.dirname(DB), exist_ok=True)
    conn = sqlite3.connect(DB)
    ensure_schema(conn)

    # Fetch data from external source and insert/update rows
    with conn:
        pass

    conn.close()

if __name__ == "__main__":
    refresh()
```

Then register with ALF scheduler:
```bash
schedule create --name "<app-name>-refresh" --schedule "0 */3 * * *" --tier direct --command "python3 ~/data/apps/<app-name>/jobs/refresh.py"
```

---

## External APIs — ALWAYS use Vault Proxy

**CRITICAL: NEVER hardcode API keys, tokens, or passwords in app code, scripts, or config files.**

If an app needs to call an external API:
- Use `vault proxy <service> <method> <path> [body]` — the vault injects credentials automatically
- Run `vault list` first to check which services are configured
- If the service isn't configured, tell the user: "Add the service via the Control Center vault page."
- NEVER ask the user for API keys or store them in files

---

## Checklist before shipping

- [ ] `app.json` exists with name, icon, description
- [ ] `index.html` uses `<link id="alf-theme-link" href="/static/theme-sage.css">` + `<script src="/static/theme-init.js">`
- [ ] Frontend uses the `api()` helper that calls `/api/bash` → curl → alf-api
- [ ] Frontend never calls `/api/<app>` directly (daemon returns 401)
- [ ] Data stored in `data/<app-name>.db` (SQLite with WAL)
- [ ] Go handler registered in alf-api (`<app>.go` + wired in `main.go`)
- [ ] alf-api rebuilt after changes (`go build -o alf-api .`)
- [ ] If app needs a persistent backend: `service.json` configured (not nohup/bootstrap)
- [ ] If app has scheduled data refresh: job writes to DB, not HTML
- [ ] No hardcoded colors — all theming via CSS variables
- [ ] No external scripts/stylesheets (CSP compliance)
- [ ] All event handlers use addEventListener (no inline onclick)

## What NOT to do

- Do NOT create apps outside `~/data/apps/`
- Do NOT use npm, webpack, or any build tooling
- Do NOT install system packages for frontend-only apps
- Do NOT create overly complex architectures — keep it simple
- Do NOT use external CDNs (CSP blocks them)
- Do NOT hardcode absolute URLs — use relative paths
- Do NOT hardcode ANY colors — use `var(--*)` exclusively
- Do NOT write dark/light theme logic — theme.css handles this automatically
- Do NOT hardcode API keys, tokens, or secrets — use `vault proxy`
- Do NOT use `nohup ... &` or `bootstrap.sh` for services — use `service.json`
