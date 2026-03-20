---
name: app-builder
description: Build standalone ALF apps — two modes: CLI binary (appsdk) or REST server (Go/Python) + web UI + manifest + marketplace publishing
version: "5"
triggers: create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application, marketplace tool, app sdk
tier: sonnet
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained and can be installed on any ALF instance via the marketplace.

**CRITICAL: Apps MUST be standalone.** No dependency on alf-api, shared databases, or external processes.

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
| **Frontend calls** | `echo JSON \| bin/<slug>` via `/api/bash` | `curl localhost:<port>` via `/api/bash` |
| **Port** | None (stdin/stdout) | Dynamic, written to `data/port` |
| **Example** | Journal | Later |

**Default to CLI binary** for tools. Use REST server only when the app needs a persistent process (websockets, background jobs, complex multi-endpoint API).

---

## Mode A: CLI Tool

### Directory structure
```
apps/<slug>/
  manifest.json      # REQUIRED — metadata + tool schema
  app.json           # REQUIRED if web UI
  index.html         # OPTIONAL — web UI
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

### Frontend — CLI tool helper

The frontend calls the tool **by name** (it's in PATH). Data dir is passed via env var:

```javascript
var SLUG = 'my-app';
var DATA = '/home/alf/data/apps/' + SLUG + '/data';

function bash(cmd) {
  return fetch('/api/bash', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
    credentials: 'same-origin',
    body: JSON.stringify({ command: cmd })
  }).then(function(r) { return r.json(); });
}

function appCmd(action, args) {
  var payload = Object.assign({ action: action }, args || {});
  var input = JSON.stringify(payload);
  var cmd = "echo '" + input.replace(/'/g, "'\\''") + "' | ALF_APP_DATA_DIR=" + DATA + " " + SLUG;
  return bash(cmd).then(function(res) {
    if (res.exit_code !== 0) throw new Error(res.error || res.output || 'Command failed');
    return res.output || '';
  });
}
```

Note: CLI tool responses use `res.exit_code`, `res.error`, and `res.output` (text, not JSON — parse if needed).

---

## Mode B: REST Server

### Directory structure
```
<slug>/
  manifest.json      # REQUIRED — metadata
  app.json           # REQUIRED if web UI
  service.json       # REQUIRED — daemon supervises the server
  index.html         # OPTIONAL — web UI
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

### Frontend — REST server helper

```javascript
let _port = null;
async function getPort() {
  if (_port) return _port;
  const res = await fetch('/api/bash', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ command: 'cat ~/data/apps/SLUG/data/port' })
  });
  const data = await res.json();
  _port = (data.output || '').trim();
  return _port;
}

async function api(method, path, body) {
  const port = await getPort();
  if (!port) throw new Error('Backend not running');
  let cmd = `curl -s -X ${method} 'http://127.0.0.1:${port}${path}'`;
  cmd += " -H 'Content-Type: application/json'";
  if (body !== undefined) {
    cmd += ` -d '${JSON.stringify(body).replace(/'/g, "'\\''")}'`;
  }
  const res = await fetch('/api/bash', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ command: cmd })
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const data = await res.json();
  if (data.error) throw new Error(data.error);
  return JSON.parse(data.output);
}
```

Note: REST server responses go through curl, so errors come from `/api/bash` as `data.error` / `data.output` (JSON string to parse).

---

## Common rules — ALL apps

### app.json (REQUIRED if web UI)
```json
{ "name": "My App", "icon": "box", "description": "Short description" }
```
`icon` MUST be a valid **Lucide** icon name in kebab-case.

### HTML head (REQUIRED)
```html
<link rel="stylesheet" id="alf-theme-link" href="/static/theme-sage.css">
<script src="/static/theme-init.js"></script>
<script src="/static/vendor/lucide.min.js"></script>
```

### CSS — theme variables only
`var(--bg)`, `var(--text)`, `var(--accent)`, `var(--bg-card)`, `var(--border)`, `var(--text-dim)`, `var(--on-accent)`, `var(--radius)`, `var(--green)`, `var(--red)`, `var(--yellow)`. NO hardcoded colors. Inline `<style>` only.

### JS rules
- NO external scripts (CSP blocks them)
- NO inline handlers (`onclick="..."`) — use `addEventListener()`
- Call `lucide.createIcons()` after every DOM update
- Icons: `<i data-lucide="icon-name"></i>`

### Icons (Lucide)
Common: `plus`, `trash-2`, `pencil`, `search`, `check`, `x`, `refresh-cw`, `save`, `download`, `upload`, `settings`, `calendar`, `clock`, `star`, `eye`, `filter`, `list`, `grid`, `bar-chart-2`, `alert-triangle`, `info`, `external-link`, `copy`, `archive`, `inbox`, `folder`, `tag`

### External APIs — Vault Proxy
**NEVER hardcode API keys.** Use `vault proxy <service> <method> <path> [body]`. Check `vault list` for available services.

---

## Publishing to marketplace

Via the **Developer** app in sidebar, or CLI:
```bash
vault proxy marketplace POST /api/apps/<slug>/publish \
  -F "manifest=<~/data/apps/<slug>/manifest.json" \
  -F "binary_amd64=@~/data/apps/<slug>/server" \
  -F "web_index.html=@~/data/apps/<slug>/index.html" \
  -F "web_app.json=@~/data/apps/<slug>/app.json" \
  -F "web_service.json=@~/data/apps/<slug>/service.json"
```

---

## Checklist before publishing

- [ ] **Standalone** — no alf-api dependency, no shared databases
- [ ] `manifest.json` valid with slug, version, description
- [ ] Tool schema: `required: ["action"]`, one tool with action enum
- [ ] `app.json` with valid Lucide icon name
- [ ] `index.html` uses theme CSS + Lucide + CSS variables only
- [ ] `index.html` uses `addEventListener` — no inline handlers
- [ ] No external scripts/stylesheets (CSP)
- [ ] **CLI tool:** binary in `~/data/tools/<slug>`, `.json` schema alongside, `chmod +x`, `--help` works
- [ ] **REST server:** `service.json` present, picks free port, writes `data/port`

## What NOT to do

- Do NOT depend on alf-api or shared databases
- Do NOT call sqlite3 CLI from the frontend — use a backend (binary or server)
- Do NOT hardcode ports — pick dynamically (REST server mode)
- Do NOT hardcode API keys — use `vault proxy`
- Do NOT use external CDN scripts or stylesheets
- Do NOT hardcode colors — always use CSS variables
- Do NOT use inline event handlers — use `addEventListener()`
- Do NOT use `nohup` or shell wrappers — use `service.json` (REST server mode)
- Do NOT forget `lucide.createIcons()` after inserting HTML with `data-lucide`
