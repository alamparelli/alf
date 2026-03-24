---
name: sdk-app-builder
description: Build standalone ALF apps — source-only (compiled at install), AlfSDK frontend, manifest, marketplace publishing
version: "4"
triggers: create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application, marketplace tool, app sdk, sdk app, new app with sdk, app with theme, interactive app
tier: sonnet
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained and can be installed on any ALF instance via the marketplace.

**CRITICAL RULES:**
- Apps MUST be **standalone** — no dependency on shared databases or external processes
- Apps are **source-only** — NEVER compile or generate binaries. Source is shipped as-is; the ALF installer compiles Go at install time on the target instance
- Use **SQLite** for all data storage — keeps apps self-contained with zero external dependencies
- Use **vanilla JS** for frontends — no frameworks, no build step, CSP-safe

## MANDATORY: Scope check

Before building, you MUST understand what the user wants. If the request has fewer than 2 concrete details, ask:
- What should the app do? What actions?
- Does it need a web UI? What should it show?
- What data does it store?

## Two architectures — pick the right one

| | CLI tool (appsdk) | REST server |
|---|---|---|
| **Best for** | Simple data tools the LLM calls | Rich web UIs, games, complex apps |
| **Backend** | Go source in `main.go` | Go/Python source with `service.json` |
| **Data** | SQLite via `$ALF_APP_DATA_DIR` | SQLite in `data/` directory |
| **Frontend** | Via `AlfSDK.tool()` | Direct fetch to server endpoints |
| **Port** | None (stdin/stdout) | Dynamic, written to `data/port` |
| **LLM tool** | Always (that's the point) | Only if LLM needs to interact |
| **Example** | Journal, Todo | Later, 2048, Wordle |

### When to use which

**CLI tool**: The LLM needs to create, read, update, or delete data (todo items, journal entries, bookmarks). The app may optionally have a web UI.

**REST server**: The app is primarily interactive for the user — games, dashboards, visual tools. The server handles all logic. A CLI tool is only needed if the LLM should also interact with the data.

**Do NOT create a CLI tool** for apps where the LLM has no reason to interact — games (wordle, 2048, snake), calculators, visual tools, etc. Omit the `tools` array from `manifest.json` entirely.

---

## Architecture A: CLI Tool (appsdk)

### Directory structure (source-only)
```
apps/<slug>/
  manifest.json      # REQUIRED — metadata + tool schema
  app.json           # REQUIRED if web UI
  index.html         # OPTIONAL — web UI (AlfSDK)
  main.go            # Go source (appsdk)
  go.mod             # Go module
  data/
    <slug>.db        # SQLite database (created at runtime)
```

At install time, ALF compiles `main.go` and places the binary in `~/data/tools/<slug>`. You NEVER do this yourself — just write the source.

A companion `<slug>.json` schema is also generated from `manifest.json` tools and placed in `~/data/tools/`. This makes the tool visible to API-based LLM tiers.

### manifest.json

One tool per app, with `action` enum for sub-commands:

```json
{
  "name": "My App",
  "slug": "my-app",
  "version": "0.1.0",
  "description": "What this app does in one line",
  "category": "productivity",
  "icon": "box",
  "tools": [
    {
      "name": "my-app",
      "description": "Tool description — be specific about what each action does",
      "parameters": {
        "type": "object",
        "properties": {
          "action": {
            "type": "string",
            "enum": ["create", "list", "delete"],
            "description": "Action to perform"
          },
          "name": { "type": "string", "description": "Item name (create)" },
          "id": { "type": "string", "description": "Item ID (delete)" }
        },
        "required": ["action"],
        "x-positional": ["action", "name", "id"]
      }
    }
  ]
}
```

Rules:
- **One tool with action enum** — not separate tools per action
- **`required: ["action"]`** — always require the action field
- **`x-positional`** — fields that become positional CLI args (in order); rest become `--key value` flags

### Go source (main.go)

```go
package main

import (
    "fmt"
    "github.com/alamparelli/alf/pkg/appsdk"
)

func main() {
    app := appsdk.New("my-app")
    app.Action("create", actionCreate)
    app.Action("list", actionList)
    app.Action("delete", actionDelete)
    app.Run()
}

func actionCreate(ctx *appsdk.Context) error {
    name := ctx.String("name")
    if name == "" {
        return fmt.Errorf("name is required")
    }
    // Use ctx.DataDir for SQLite path
    appsdk.Respond(fmt.Sprintf("Created: %s", name))
    return nil
}
```

SDK patterns:
- `ctx.String("key")` — string arg or `""`
- `ctx.Int("key", default)` — int arg with fallback
- `ctx.DataDir` — persistent storage path (`$ALF_APP_DATA_DIR`)
- `appsdk.Respond(text)` — text output
- `appsdk.RespondJSON(v)` — JSON output
- `appsdk.Fail(msg)` — error to stderr + exit 1

**The CLI tool MUST also follow the `tool-creator` skill conventions** (read `skills.d/tool-creator/SKILL.md`): `--help` flag, error handling, output conventions, JSON schema with `x-positional`.

---

## Architecture B: REST Server

### Directory structure (source-only)
```
apps/<slug>/
  manifest.json      # REQUIRED — metadata (no tools unless LLM needs access)
  app.json           # REQUIRED — web UI metadata
  service.json       # REQUIRED — daemon supervises the server
  index.html         # Web UI (AlfSDK)
  main.go            # Go server source
  go.mod             # Go module
  data/
    <slug>.db        # SQLite database (created at runtime)
    port             # Port file (written by server at startup)
```

At install time, ALF compiles `main.go` into a `server` binary in the app directory. You NEVER compile it yourself.

### service.json

```json
{
  "name": "My App API",
  "command": "./server",
  "restart": "always",
  "restart_delay": "3s",
  "max_restarts": 100,
  "enabled": true
}
```

For Python apps: `"command": "python3", "args": ["server.py"]` — no compilation needed.

The daemon auto-supervises: restart on crash, start on boot, SIGTERM on shutdown.

### Server requirements

The backend MUST:
- Pick a free port and write it to `data/port`
- Use **SQLite** in `data/` for all persistent storage
- Expose JSON REST endpoints
- Listen on `127.0.0.1` only

### Go server template (main.go)

```go
package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net"
    "net/http"
    "os"
    "path/filepath"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    _ "modernc.org/sqlite"
)

var appDir string

func init() {
    appDir = filepath.Dir(os.Args[0])
    if appDir == "" || appDir == "." {
        appDir, _ = os.Getwd()
    }
}

func main() {
    dataDir := filepath.Join(appDir, "data")
    os.MkdirAll(dataDir, 0755)
    dbPath := filepath.Join(dataDir, "SLUG.db")

    db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
    if err != nil { log.Fatal(err) }
    db.SetMaxOpenConns(1)

    db.Exec(`CREATE TABLE IF NOT EXISTS items (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
        created_at TEXT DEFAULT (datetime('now'))
    )`)

    r := chi.NewRouter()
    r.Use(middleware.Recoverer)
    r.Get("/api/SLUG", listItems(db))
    r.Post("/api/SLUG", createItem(db))
    r.Delete("/api/SLUG/{id}", deleteItem(db))

    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil { log.Fatal(err) }
    port := ln.Addr().(*net.TCPAddr).Port
    os.WriteFile(filepath.Join(dataDir, "port"), []byte(fmt.Sprintf("%d", port)), 0644)
    log.Printf("SLUG server listening on :%d", port)
    log.Fatal(http.Serve(ln, r))
}
```

### Python server template (server.py)

```python
#!/usr/bin/env python3
import json, os, socket, sqlite3
from http.server import HTTPServer, BaseHTTPRequestHandler

APP_DIR = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(APP_DIR, "data")
DB_PATH = os.path.join(DATA_DIR, "SLUG.db")
os.makedirs(DATA_DIR, exist_ok=True)

def get_db():
    conn = sqlite3.connect(DB_PATH)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.row_factory = sqlite3.Row
    return conn

with get_db() as conn:
    conn.execute("""CREATE TABLE IF NOT EXISTS items (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
        created_at TEXT DEFAULT (datetime('now'))
    )""")

class Handler(BaseHTTPRequestHandler):
    def _read_body(self):
        length = int(self.headers.get("Content-Length", 0))
        return json.loads(self.rfile.read(length)) if length else {}
    def _respond(self, code, data):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())
    def do_GET(self):
        if self.path == "/api/SLUG":
            with get_db() as conn:
                rows = conn.execute("SELECT * FROM items ORDER BY id DESC").fetchall()
            self._respond(200, [dict(r) for r in rows])
        else: self._respond(404, {"error": "not found"})
    def do_POST(self):
        if self.path == "/api/SLUG":
            body = self._read_body()
            with get_db() as conn:
                cur = conn.execute("INSERT INTO items (title) VALUES (?)", (body.get("title", ""),))
            self._respond(201, {"id": cur.lastrowid})
        else: self._respond(404, {"error": "not found"})
    def log_message(self, fmt, *args): pass

def find_free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]

if __name__ == "__main__":
    port = find_free_port()
    with open(os.path.join(DATA_DIR, "port"), "w") as f: f.write(str(port))
    print(f"SLUG server listening on :{port}")
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
```

For Python: `"command": "python3", "args": ["server.py"]` in service.json.

---

## Frontend — AlfSDK (REQUIRED for all apps with UI)

All app frontends use the **AlfSDK** for parent SPA communication (theme sync, toast, navigation). Uses **vanilla JS** — no framework, no build step, CSP-safe.

### Frontend Template

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My App</title>
  <link rel="stylesheet" href="/static/style.css">
  <link rel="stylesheet" id="alf-theme" href="/static/theme-sage.css">
  <script src="/static/theme-init.js"></script>
  <script src="/static/alf-app-sdk.js"></script>
  <style>
    body { padding: 1.5rem; font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }
    .card { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius, 8px); padding: 1.25rem; margin-bottom: 1rem; }
    .btn { display: inline-flex; align-items: center; gap: 6px; padding: 6px 14px; border: 1px solid var(--border); border-radius: var(--radius, 8px); background: var(--bg-input); color: var(--text); font-family: inherit; font-size: 0.85rem; cursor: pointer; }
    .btn-primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
    .btn:disabled { opacity: 0.5; cursor: not-allowed; }
    .form-row { margin-bottom: 0.75rem; }
    .form-row label { display: block; font-size: 0.8rem; font-weight: 500; margin-bottom: 4px; }
    .form-row input, .form-row textarea { width: 100%; padding: 8px; border: 1px solid var(--border); border-radius: var(--radius, 8px); background: var(--bg-input); color: var(--text); font-family: inherit; font-size: 0.85rem; }
    .form-row textarea { min-height: 100px; resize: vertical; }
    .actions { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 0.75rem; }
    .empty { color: var(--text-dim); padding: 2rem; text-align: center; }
    /* app-specific styles below */
  </style>
</head>
<body>
  <h2>My App</h2>
  <div id="list"></div>
  <div id="editor" style="display:none"></div>
  <div class="actions" id="toolbar">
    <button class="btn btn-primary" onclick="showEditor()">Add Item</button>
  </div>

  <script>
    var SLUG = 'my-app'; // REPLACE with actual slug

    AlfSDK.init({
      slug: SLUG,
      onThemeChange: function(palette) {
        var link = document.getElementById('alf-theme');
        if (link) link.href = '/static/theme-' + palette + '.css';
      }
    });

    var items = [];

    // For CLI tool apps: use AlfSDK.tool()
    function load() {
      AlfSDK.tool('list').then(function(out) {
        try { items = JSON.parse(out); } catch(e) { items = []; }
        renderList();
      });
    }

    // For REST server apps: use direct fetch to local server
    // function load() {
    //   fetch('/apps/SLUG/api/items').then(r => r.json()).then(function(data) {
    //     items = data;
    //     renderList();
    //   });
    // }

    function renderList() {
      var el = document.getElementById('list');
      if (!items || !items.length) {
        el.innerHTML = '<p class="empty">No items yet.</p>';
        return;
      }
      el.innerHTML = items.map(function(item) {
        return '<div class="card"><strong>' + esc(item.name) + '</strong></div>';
      }).join('');
    }

    function esc(s) {
      if (!s) return '';
      var d = document.createElement('div');
      d.textContent = s;
      return d.innerHTML;
    }

    load();
  </script>
</body>
</html>
```

### AlfSDK Reference

The SDK is loaded from `/static/alf-app-sdk.js`. Available methods:

| Method | Description |
|---|---|
| `AlfSDK.init({ slug, onThemeChange })` | Initialize. Call once on load. |
| `AlfSDK.tool(action, args)` | Run CLI tool with action + args. Returns output string. |
| `AlfSDK.api(path, opts)` | Authenticated fetch (same-origin cookies). |
| `AlfSDK.bash(cmd)` | Execute shell command via `/api/bash`. |
| `AlfSDK.navigate(view)` | Navigate parent SPA (e.g. `'chat'`, `'settings'`). |
| `AlfSDK.toast(msg, type)` | Show toast in parent (`'success'`, `'error'`, `'info'`). |
| `AlfSDK.getTheme()` | Returns `{ palette, dark }`. |

---

## Common rules — ALL apps

### app.json (REQUIRED if web UI)
```json
{ "name": "My App", "icon": "box", "description": "Short description" }
```
`icon` MUST be a valid **Lucide** icon name in kebab-case.

### Data storage — SQLite only
- Use **SQLite** (`modernc.org/sqlite` for Go, `sqlite3` for Python) for all persistent data
- Store the database in `data/<slug>.db` within the app directory
- Always use `WAL` journal mode for concurrent access
- No external databases (Postgres, Redis, etc.) — apps must be fully standalone

### CSS — theme variables only
`var(--bg)`, `var(--text)`, `var(--accent)`, `var(--bg-card)`, `var(--border)`, `var(--text-dim)`, `var(--on-accent)`, `var(--radius)`, `var(--green)`, `var(--red)`, `var(--yellow)`, `var(--mauve)`, `var(--sapphire)`. NO hardcoded colors. Inline `<style>` only.

### Frontend rules
1. **Always init AlfSDK** at the top of your script
2. **Always include onThemeChange** to sync theme from parent
3. **Use CSS variables** from the theme — never hardcode colors
4. **Set font-family explicitly**: `system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif` (Google Fonts blocked by CSP in iframes)
5. **Load `/static/style.css`** for base styles and `/static/theme-*.css` for theme colors
6. **Load `/static/theme-init.js`** for FOUC prevention
7. **No build step** — single HTML file, vanilla JS only
8. **No `unsafe-eval`** — do NOT use frameworks that require `new Function()` (Petite Vue, Vue, Angular). CSP blocks them.
9. **No external scripts/stylesheets** (CSP blocks them)

### External APIs — Vault Proxy
**NEVER hardcode API keys.** Use `vault proxy <service> <method> <path> [body]`. Check `vault list` for available services.

---

## Checklist before publishing

- [ ] **Source-only** — no compiled binaries in the app directory
- [ ] **Standalone** — own server/tool, SQLite for data, no shared databases
- [ ] `manifest.json` valid with slug, version, description
- [ ] `app.json` with valid Lucide icon name (if web UI)
- [ ] `index.html` uses AlfSDK + theme CSS + CSS variables + explicit font-family
- [ ] No external scripts/stylesheets (CSP)
- [ ] Go source has `go.mod` with all dependencies declared
- [ ] **CLI tool (if LLM needs access):** `main.go` with appsdk, tool schema in `manifest.json` tools
- [ ] **REST server (if needed):** `service.json` present, picks free port, writes `data/port`

## What NOT to do

- Do NOT compile binaries — source ships as-is, ALF compiles at install time
- Do NOT depend on shared databases or external services
- Do NOT use databases other than SQLite — no Postgres, Redis, etc.
- Do NOT call sqlite3 CLI from the frontend — use a backend (source or server)
- Do NOT hardcode ports — pick dynamically (REST server mode)
- Do NOT hardcode API keys — use `vault proxy`
- Do NOT use external CDN scripts or stylesheets
- Do NOT hardcode colors — always use CSS variables
- Do NOT use `font-family: inherit` — set fonts explicitly (Google Fonts not available in iframes)
- Do NOT use `nohup` or shell wrappers — use `service.json` (REST server mode)
- Do NOT use frameworks that need `unsafe-eval` (Petite Vue, Vue, Angular)
- Do NOT create a CLI tool for apps the LLM doesn't need to interact with (games, visual tools)
