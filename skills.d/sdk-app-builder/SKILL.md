---
name: sdk-app-builder
description: Build standalone ALF apps — CLI binary (appsdk) or REST server + AlfSDK frontend + manifest + marketplace publishing
version: "3"
triggers: create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application, marketplace tool, app sdk, sdk app, new app with sdk, app with theme, interactive app
tier: sonnet
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained and can be installed on any ALF instance via the marketplace.

**CRITICAL: Apps MUST be standalone.** No dependency on shared databases or external processes. Each app runs its own server (Go/Python, or user's choice).

## MANDATORY: Scope check

Before building, you MUST understand what the user wants. If the request has fewer than 2 concrete details, ask:
- What actions should the tool support?
- Does it need a web UI? What should it show?
- What data does it store?

## Two modes — pick the right one

| | CLI binary (appsdk) | REST server |
|---|---|---|
| **Best for** | Simple tools, LLM-callable actions | Complex apps with rich web UIs |
| **Backend** | Single Go binary in `bin/<slug>` | Go/Python server with `service.json` |
| **Data access** | `$ALF_APP_DATA_DIR` env var | Relative `data/` directory |
| **Frontend calls** | Via `AlfSDK.tool()` | Via `AlfSDK.bash()` + curl |
| **Port** | None (stdin/stdout) | Dynamic, written to `data/port` |
| **Example** | Journal | Later |

**Default to CLI binary** for tools. Use REST server only when the app needs a persistent process (websockets, background jobs, complex multi-endpoint API).

**IMPORTANT: Every app MUST include a CLI tool binary** (`bin/<slug>`) — even REST server apps. This is how the LLM interacts with the app's data (write, read, search, delete). The CLI tool is separate from the server binary. Without it, the LLM cannot use the app as a tool. The CLI binary must be declared in `manifest.json` under `tools` with its action schema.

---

## Mode A: CLI Tool

### Directory structure
```
apps/<slug>/
  manifest.json      # REQUIRED — metadata + tool schema
  app.json           # REQUIRED if web UI
  index.html         # OPTIONAL — web UI (AlfSDK)
  data/
    <slug>.db        # SQLite database

tools/
  <slug>             # Executable binary or script (in PATH)
  <slug>.json        # JSON schema for API-based LLM tiers
```

The CLI tool binary goes in `~/data/tools/` (NOT inside the app directory). It is automatically in PATH and callable by name. The companion `.json` schema makes it visible to API-based LLM tiers.

If the app has data, the tool reads/writes `~/data/apps/<slug>/data/` via `$ALF_APP_DATA_DIR` or by convention.

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
        "required": ["action"]
      }
    }
  ]
}
```

Rules:
- **One tool with action enum** — not separate tools per action
- **`required: ["action"]`** — always require the action field

### Tool binary and JSON schema (in ~/data/tools/)

**The CLI tool MUST follow the `tool-creator` skill conventions.** Read the `tool-creator` skill (in `skills.d/tool-creator/SKILL.md`) for the full standard: shebang, `--help` flag, error handling, output conventions, naming, and JSON schema with `x-positional`.

Create `~/data/tools/<slug>` (the binary) and `~/data/tools/<slug>.json` (the schema) alongside it:

```json
{
  "name": "my-app",
  "description": "Short description of what the tool does.",
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
```

### Go binary (appsdk)

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

Build and install:
```bash
export PATH="/home/alf/data/tools/go-sdk/bin:$PATH"
export GOPATH="/home/alf/data/tools/go-path"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ~/data/tools/<slug> .
chmod +x ~/data/tools/<slug>
```

---

## Mode B: REST Server

### Directory structure
```
<slug>/
  manifest.json      # REQUIRED — metadata
  app.json           # REQUIRED if web UI
  service.json       # REQUIRED — daemon supervises the server
  index.html         # OPTIONAL — web UI (AlfSDK)
  server.go          # Go backend (or server.py for Python)
  go.mod             # Go module (Go apps only)
  server             # Compiled binary
  data/
    <slug>.db        # SQLite database
    port             # Port file (written by server at startup)
```

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

For Python: `"command": "python3", "args": ["server.py"]`

The daemon auto-supervises: restart on crash, start on boot, SIGTERM on shutdown.

### Server requirements

The backend MUST:
- Pick a free port and write it to `data/port`
- Create/manage its own SQLite database in `data/`
- Expose JSON REST endpoints
- Listen on `127.0.0.1` only

### Go server template

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

Build:
```bash
export PATH="/home/alf/data/tools/go-sdk/bin:$PATH"
export GOPATH="/home/alf/data/tools/go-path"
cd ~/data/apps/<slug>
go mod init app && go mod tidy && go build -o server .
```

### Python server template

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
    body { padding: 1.5rem; }
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

    function load() {
      AlfSDK.tool('list').then(function(out) {
        try { items = JSON.parse(out); } catch(e) { items = []; }
        renderList();
      }).catch(function(e) {
        document.getElementById('list').innerHTML = '<p class="empty">Error: ' + esc(e.message) + '</p>';
      });
    }

    function renderList() {
      var el = document.getElementById('list');
      if (!items || !items.length) {
        el.innerHTML = '<p class="empty">No items yet.</p>';
        return;
      }
      el.innerHTML = items.map(function(item) {
        return '<div class="card" onclick="editItem(' + item.id + ')">' +
          '<strong>' + esc(item.name) + '</strong>' +
          '</div>';
      }).join('');
    }

    function save(id) {
      var name = document.getElementById('fName').value.trim();
      if (!name) { AlfSDK.toast('Name required', 'error'); return; }
      var action = id ? 'update' : 'create';
      var args = { name: name };
      if (id) args.id = String(id);
      AlfSDK.tool(action, args).then(function() {
        AlfSDK.toast('Saved', 'success');
        backToList();
      }).catch(function(e) { AlfSDK.toast(e.message, 'error'); });
    }

    function remove(id) {
      if (!confirm('Delete this item?')) return;
      AlfSDK.tool('delete', { id: String(id) }).then(function() {
        AlfSDK.toast('Deleted');
        backToList();
      }).catch(function(e) { AlfSDK.toast(e.message, 'error'); });
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

### Frontend for REST server apps

For REST server apps, use `AlfSDK.bash()` with curl to call the local server:

```javascript
var _port = null;
function getPort() {
  if (_port) return Promise.resolve(_port);
  return AlfSDK.bash('cat ~/data/apps/SLUG/data/port').then(function(out) {
    _port = out.trim();
    return _port;
  });
}

function apiCall(method, path, body) {
  return getPort().then(function(port) {
    if (!port) throw new Error('Backend not running');
    var cmd = "curl -s -X " + method + " 'http://127.0.0.1:" + port + path + "'";
    cmd += " -H 'Content-Type: application/json'";
    if (body !== undefined) {
      cmd += " -d '" + JSON.stringify(body).replace(/'/g, "'\\''") + "'";
    }
    return AlfSDK.bash(cmd);
  }).then(function(out) {
    return JSON.parse(out);
  });
}
```

---

## Common rules — ALL apps

### app.json (REQUIRED if web UI)
```json
{ "name": "My App", "icon": "box", "description": "Short description" }
```
`icon` MUST be a valid **Lucide** icon name in kebab-case.

### CSS — theme variables only
`var(--bg)`, `var(--text)`, `var(--accent)`, `var(--bg-card)`, `var(--border)`, `var(--text-dim)`, `var(--on-accent)`, `var(--radius)`, `var(--green)`, `var(--red)`, `var(--yellow)`. NO hardcoded colors. Inline `<style>` only.

### Frontend rules
1. **Always use AlfSDK.tool()** for CLI tool calls — never raw `fetch('/api/bash')`
2. **Always init AlfSDK** at the top of your script
3. **Always include onThemeChange** to sync theme from parent
4. **Use CSS variables** from the theme — never hardcode colors
5. **Load `/static/style.css`** for base styles and `/static/theme-*.css` for theme colors
6. **Load `/static/theme-init.js`** for FOUC prevention
7. **No build step** — single HTML file, vanilla JS only
8. **No `unsafe-eval`** — do NOT use frameworks that require `new Function()` (Petite Vue, Vue, Angular). CSP blocks them.
9. **No external scripts/stylesheets** (CSP blocks them)

### External APIs — Vault Proxy
**NEVER hardcode API keys.** Use `vault proxy <service> <method> <path> [body]`. Check `vault list` for available services.

---

## Checklist before publishing

- [ ] **Standalone** — own server (Go/Python/user choice), no shared databases
- [ ] `manifest.json` valid with slug, version, description
- [ ] Tool schema: `required: ["action"]`, one tool with action enum
- [ ] `app.json` with valid Lucide icon name
- [ ] `index.html` uses AlfSDK + theme CSS + CSS variables only
- [ ] No external scripts/stylesheets (CSP)
- [ ] **CLI tool (always required):** `bin/<slug>` binary with `--help`, declared in `manifest.json` tools
- [ ] **REST server (if needed):** `service.json` present, picks free port, writes `data/port`

## What NOT to do

- Do NOT depend on shared databases or external services
- Do NOT call sqlite3 CLI from the frontend — use a backend (binary or server)
- Do NOT hardcode ports — pick dynamically (REST server mode)
- Do NOT hardcode API keys — use `vault proxy`
- Do NOT use external CDN scripts or stylesheets
- Do NOT hardcode colors — always use CSS variables
- Do NOT use `nohup` or shell wrappers — use `service.json` (REST server mode)
- Do NOT use frameworks that need `unsafe-eval` (Petite Vue, Vue, Angular)
