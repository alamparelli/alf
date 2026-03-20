---
name: app-builder
description: Build standalone ALF apps — own backend (Go/Python), web UI, SQLite, marketplace-ready
version: "4"
triggers: create app, new app, build app, make app, web app, marketplace app, publish app, standalone app, webapp, build application, create application
tier: sonnet
---

# ALF App Builder

You build **standalone** apps for ALF. Every app is self-contained: own backend server, own web UI, own data. Apps can be used locally or published to the ALF Marketplace.

**CRITICAL: Apps MUST be standalone.** No dependency on alf-api, shared databases, or external processes. Everything the app needs lives in its own directory.

---

## Before building — MANDATORY scope check

**NEVER start building unless the user has described at least one concrete feature or data field.**

"Create a habit tracker" or "build me a crypto app" is NOT enough — just a name with no spec.

**Rule: if the request has fewer than 2 concrete details, ask 2-3 targeted questions first:**
- What are the core features? (e.g. "track daily habits with streaks" vs "habit templates with categories")
- What data does it work with? (user input? external API? scheduled feed?)
- Preferred backend language? (Go default, Python alternative)
- Any must-haves? (specific fields, calculations, integrations)

**Examples that require questions first:**
- "create a crypto app" → portfolio tracker? price alerts? trading journal? which APIs?
- "build a habit tracker" → what do you track? streaks? categories? reminders?

**Examples specific enough to build directly:**
- "create a reading list with title, author, status, rating and a form to add books"
- "build a crypto portfolio tracker showing holdings, current price from CoinGecko, and P&L"

**When the user references an existing app** (by name or slug), work on that app — don't create a new one. Check `~/data/apps/` for existing app directories.

---

## Architecture

Every app with data follows this pattern:

```
┌─────────────┐  /api/bash + curl   ┌──────────────┐  SQLite  ┌──────────────┐
│  index.html │ ──────────────────► │  app backend │ ◄──────► │  data/app.db │
│  (browser)  │ ◄────────────────── │  (Go/Python) │          └──────────────┘
└─────────────┘       JSON          └──────┬───────┘
                                           │
                                    ┌──────┴───────┐
                                    │  data/port   │
                                    └──────────────┘
```

1. **Backend** starts via `service.json`, picks a free port, writes it to `data/port`
2. **Frontend** reads the port via `/api/bash`, then calls the backend via `/api/bash` + curl
3. **Backend** owns its own SQLite DB, routes, and business logic
4. **No shared state** — the app is fully self-contained

---

## App directory structure

Create everything under `~/data/apps/<slug>/`:

```
<slug>/
├── app.json           # REQUIRED — sidebar display (name, icon, description)
├── index.html         # REQUIRED — single-file web UI
├── manifest.json      # REQUIRED for marketplace — metadata + tool schema
├── service.json       # REQUIRED if backend — daemon auto-supervises it
├── server.go          # Backend source (Go)
├── go.mod             # Go module (Go apps only)
├── server             # Compiled binary (Go apps only)
├── server.py          # Backend source (Python alternative)
├── bin/<slug>         # OPTIONAL — CLI tool binary (SDK apps)
└── data/
    ├── <slug>.db      # SQLite database (created by backend on first run)
    └── port           # Port file (written by backend at startup)
```

**No files outside this directory. No shared state.**

---

## app.json (REQUIRED)

```json
{
  "name": "Human-Readable Name",
  "icon": "lucide-icon-name",
  "description": "One-line description of what this app does"
}
```

`icon` MUST be a valid **Lucide** icon name in kebab-case (see Icons section below).

---

## manifest.json (REQUIRED for marketplace)

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
      "description": "Tool description for the LLM — be specific about each action",
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
  ],
  "service": {
    "command": "./server",
    "port": 0
  }
}
```

### Tool schema rules:
- **NO `x-positional`** — SDK tools read JSON from stdin
- **One tool with action enum** — not separate tools per action
- **`required: ["action"]`** — always require the action field
- **Per-action param descriptions** — add "(create)" or "(delete)" suffix

---

## service.json (REQUIRED if backend)

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

| Field | Description | Default |
|-------|-------------|---------|
| `command` | Executable (relative to app dir, must stay within directory) | Required |
| `args` | Command-line arguments | `[]` |
| `env` | Environment variables (PATH, HOME, LD_* blocked) | `{}` |
| `restart` | `"always"`, `"on-failure"`, or `"no"` | `"always"` |
| `restart_delay` | Base delay (exponential backoff up to 60s) | `"3s"` |
| `max_restarts` | Max restart attempts | `100` |
| `enabled` | Auto-start on boot | `true` |

**Do NOT use `nohup ... &` or shell scripts — use `service.json`.**

---

## Backend server

The backend is a REST API server. It MUST:
- Pick a free port and write it to `data/port`
- Create/manage its own SQLite database in `data/`
- Expose JSON REST endpoints
- Listen on `127.0.0.1` only

### Go backend template

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
	exe, _ := os.Executable()
	appDir = filepath.Dir(exe)
	if appDir == "" || appDir == "." {
		appDir, _ = os.Getwd()
	}
}

func main() {
	dataDir := filepath.Join(appDir, "data")
	os.MkdirAll(dataDir, 0755)
	dbPath := filepath.Join(dataDir, "SLUG.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		log.Fatal(err)
	}
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
	if err != nil {
		log.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	os.WriteFile(filepath.Join(dataDir, "port"), []byte(fmt.Sprintf("%d", port)), 0644)
	log.Printf("SLUG server listening on :%d", port)
	log.Fatal(http.Serve(ln, r))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func listItems(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), "SELECT id, title, created_at FROM items ORDER BY id DESC")
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id int64
			var title, created string
			rows.Scan(&id, &title, &created)
			items = append(items, map[string]any{"id": id, "title": title, "created_at": created})
		}
		writeJSON(w, 200, items)
	}
}

func createItem(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Title string `json:"title"` }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Title == "" {
			writeJSON(w, 400, map[string]string{"error": "title required"})
			return
		}
		res, _ := db.Exec("INSERT INTO items (title) VALUES (?)", body.Title)
		id, _ := res.LastInsertId()
		writeJSON(w, 201, map[string]any{"id": id})
	}
}

func deleteItem(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		db.Exec("DELETE FROM items WHERE id=?", id)
		writeJSON(w, 200, map[string]any{"ok": true})
	}
}
```

Build:
```bash
export PATH="/home/alf/data/tools/go-sdk/bin:$PATH"
export GOPATH="/home/alf/data/tools/go-path"
cd ~/data/apps/<slug>
go mod init app
go mod tidy
go build -o server .
```

### Python backend template

```python
#!/usr/bin/env python3
"""Standalone app backend."""
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

# Init schema
with get_db() as conn:
    conn.execute("""CREATE TABLE IF NOT EXISTS items (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
        created_at TEXT DEFAULT (datetime('now'))
    )""")

class Handler(BaseHTTPRequestHandler):
    def _body(self):
        n = int(self.headers.get("Content-Length", 0))
        return json.loads(self.rfile.read(n)) if n else {}

    def _json(self, code, data):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def do_GET(self):
        if self.path == "/api/SLUG":
            with get_db() as c:
                rows = c.execute("SELECT * FROM items ORDER BY id DESC").fetchall()
            self._json(200, [dict(r) for r in rows])
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path == "/api/SLUG":
            body = self._body()
            with get_db() as c:
                cur = c.execute("INSERT INTO items (title) VALUES (?)", (body.get("title", ""),))
            self._json(201, {"id": cur.lastrowid})
        else:
            self._json(404, {"error": "not found"})

    def do_DELETE(self):
        if self.path.startswith("/api/SLUG/"):
            item_id = self.path.split("/")[-1]
            with get_db() as c:
                c.execute("DELETE FROM items WHERE id=?", (item_id,))
            self._json(200, {"ok": True})
        else:
            self._json(404, {"error": "not found"})

    def log_message(self, fmt, *args): pass

if __name__ == "__main__":
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    with open(os.path.join(DATA_DIR, "port"), "w") as f:
        f.write(str(port))
    print(f"SLUG server listening on :{port}")
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
```

---

## Frontend (index.html)

### Theming — CSS variables (MANDATORY)

```html
<link rel="stylesheet" id="alf-theme-link" href="/static/theme-sage.css">
<script src="/static/theme-init.js"></script>
```

Use ONLY these variables — **never hardcode colors**:
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

### Icons — Lucide (MANDATORY)

```html
<script src="/static/vendor/lucide.min.js"></script>
```

Use `<i data-lucide="icon-name"></i>` in HTML, then call `lucide.createIcons()` after DOM ready and after every dynamic DOM update.

Common icons: `plus`, `trash-2`, `pencil`, `search`, `check`, `x`, `refresh-cw`, `save`, `download`, `upload`, `settings`, `calendar`, `clock`, `star`, `heart`, `eye`, `filter`, `list`, `grid`, `bar-chart-2`, `trending-up`, `alert-triangle`, `info`, `chevron-down`, `chevron-right`, `external-link`, `copy`, `archive`, `inbox`, `folder`, `tag`, `book-open`, `globe`, `zap`

Set `.lucide { width: 16px; height: 16px; }` for consistent sizing.

### API helper — port discovery + curl bridge

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
  let cmd = `curl -sf -X ${method} http://127.0.0.1:${port}${path}`;
  cmd += " -H 'Content-Type: application/json'";
  if (body !== undefined) {
    cmd += ` -d '${JSON.stringify(body).replace(/'/g, "'\\''")}'`;
  }
  const res = await fetch('/api/bash', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ command: cmd })
  });
  const data = await res.json();
  if (data.exit_code !== 0) throw new Error(data.error || 'request failed');
  return data.output ? JSON.parse(data.output) : null;
}
```

### Security constraints (enforced by CSP)
- No external scripts (`<script src="https://...">` blocked)
- No inline event handlers (`onclick="..."` blocked) — use `addEventListener()`
- No external stylesheets except `/static/theme-*.css`
- Fetch/XHR restricted to same origin (`/api/*` only)

### Full HTML template

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>APP_NAME</title>
  <link rel="stylesheet" id="alf-theme-link" href="/static/theme-sage.css">
  <script src="/static/theme-init.js"></script>
  <script src="/static/vendor/lucide.min.js"></script>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, -apple-system, sans-serif; padding: 24px; background: var(--bg); color: var(--text); }
    .header { display: flex; align-items: center; gap: 12px; margin-bottom: 20px; }
    .header h1 { color: var(--accent); font-size: 1.4rem; }
    .card { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; margin-bottom: 12px; display: flex; align-items: center; justify-content: space-between; }
    .btn { display: inline-flex; align-items: center; gap: 6px; padding: 7px 14px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg-card); color: var(--text); cursor: pointer; font-size: 0.85rem; }
    .btn:hover { border-color: var(--accent); color: var(--accent); }
    .btn-primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
    .btn-sm { padding: 4px 8px; font-size: 0.78rem; }
    .btn-danger:hover { border-color: var(--red); color: var(--red); }
    .input { width: 100%; padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg); color: var(--text); font-size: 0.85rem; }
    .empty { text-align: center; color: var(--text-dim); padding: 40px; }
    .error { text-align: center; color: var(--red); padding: 40px; }
    .lucide { width: 16px; height: 16px; }
  </style>
</head>
<body>
  <div class="header">
    <h1>APP_NAME</h1>
    <button class="btn btn-primary" id="addBtn"><i data-lucide="plus"></i> Add</button>
    <button class="btn" id="refreshBtn"><i data-lucide="refresh-cw"></i></button>
  </div>
  <div id="app"><div class="empty">Loading...</div></div>

  <script>
    var SLUG = 'APP_SLUG';

    /* ── Port discovery + API helper ── */
    let _port = null;
    async function getPort() {
      if (_port) return _port;
      const res = await fetch('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: 'cat ~/data/apps/' + SLUG + '/data/port' })
      });
      const data = await res.json();
      _port = (data.output || '').trim();
      return _port;
    }

    async function api(method, path, body) {
      const port = await getPort();
      if (!port) throw new Error('Backend not running');
      let cmd = 'curl -sf -X ' + method + ' http://127.0.0.1:' + port + path;
      cmd += " -H 'Content-Type: application/json'";
      if (body !== undefined) cmd += " -d '" + JSON.stringify(body).replace(/'/g, "'\\''" ) + "'";
      const res = await fetch('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd })
      });
      const data = await res.json();
      if (data.exit_code !== 0) throw new Error(data.error || 'request failed');
      return data.output ? JSON.parse(data.output) : null;
    }

    function esc(s) { var d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

    /* ── App logic ── */
    async function load() {
      try {
        var items = await api('GET', '/api/' + SLUG);
        render(Array.isArray(items) ? items : []);
      } catch(e) {
        document.getElementById('app').innerHTML =
          '<div class="error"><i data-lucide="alert-triangle"></i> ' + esc(e.message) + '</div>';
        lucide.createIcons();
      }
    }

    function render(items) {
      var app = document.getElementById('app');
      if (!items.length) {
        app.innerHTML = '<div class="empty"><i data-lucide="inbox"></i><p style="margin-top:8px">No items yet</p></div>';
      } else {
        app.innerHTML = items.map(function(item) {
          return '<div class="card">' +
            '<span>' + esc(item.title || '') + '</span>' +
            '<button class="btn btn-sm btn-danger" data-del="' + item.id + '"><i data-lucide="trash-2"></i></button>' +
          '</div>';
        }).join('');
        app.querySelectorAll('[data-del]').forEach(function(btn) {
          btn.addEventListener('click', function() {
            api('DELETE', '/api/' + SLUG + '/' + btn.dataset.del).then(load);
          });
        });
      }
      lucide.createIcons();
    }

    document.getElementById('addBtn').addEventListener('click', async function() {
      var title = prompt('Title:');
      if (!title) return;
      await api('POST', '/api/' + SLUG, { title: title });
      load();
    });

    document.getElementById('refreshBtn').addEventListener('click', load);

    load();
    lucide.createIcons();
  </script>
</body>
</html>
```

---

## CLI tool (optional — SDK pattern)

If the app needs a CLI tool callable by the LLM (in addition to or instead of a web backend), use the App SDK:

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
- `ctx.DataDir` — persistent storage path
- `appsdk.Respond(text)` / `appsdk.RespondJSON(v)` / `appsdk.Fail(msg)`

Build: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ~/data/apps/<slug>/bin/<slug> .`

---

## External APIs — ALWAYS use Vault Proxy

**NEVER hardcode API keys, tokens, or passwords.**
- Use `vault proxy <service> <method> <path> [body]`
- Check `vault list` for available services
- If not configured, tell user to add via Control Center vault page

---

## Publishing to marketplace

Once ready:

1. Open the **Developer** app in sidebar → select app → **Publish**

Or via CLI:
```bash
vault proxy marketplace POST /api/apps/<slug>/publish \
  -F "manifest=<~/data/apps/<slug>/manifest.json" \
  -F "binary_amd64=@~/data/apps/<slug>/server" \
  -F "web_index.html=@~/data/apps/<slug>/index.html" \
  -F "web_app.json=@~/data/apps/<slug>/app.json" \
  -F "web_service.json=@~/data/apps/<slug>/service.json"
```

---

## Checklist before shipping

- [ ] `app.json` with name, valid Lucide icon, description
- [ ] `index.html` loads theme CSS + Lucide + uses CSS variables only
- [ ] `index.html` calls `lucide.createIcons()` after every DOM update
- [ ] `index.html` uses `addEventListener` — no inline handlers
- [ ] No external scripts/stylesheets (CSP compliance)
- [ ] **Standalone** — no alf-api dependency, no shared databases
- [ ] `service.json` configured for backend process
- [ ] Backend picks a free port, writes to `data/port`
- [ ] Backend creates its own SQLite DB in `data/` with WAL mode
- [ ] Frontend uses `getPort()` + `/api/bash` + curl bridge
- [ ] No hardcoded paths — resolve relative to app directory
- [ ] No hardcoded API keys — use `vault proxy`

### Additional for marketplace publishing:
- [ ] `manifest.json` with slug, version, description, tool schema
- [ ] Tool schema: `required: ["action"]`, no `x-positional`, one tool with action enum
- [ ] Tested: enable via Marketplace, tool works in chat, web UI works in sidebar

## What NOT to do

- Do NOT depend on alf-api or shared databases — app must be standalone
- Do NOT hardcode ports — always pick a free port dynamically
- Do NOT hardcode colors — always use `var(--*)` CSS variables
- Do NOT use external CDN scripts or stylesheets (CSP blocks them)
- Do NOT use inline event handlers (`onclick="..."`) — use `addEventListener()`
- Do NOT forget `lucide.createIcons()` after inserting HTML with `data-lucide`
- Do NOT hardcode API keys — use `vault proxy`
- Do NOT use `nohup` or shell wrappers — use `service.json`
- Do NOT use npm, webpack, or build tooling
- Do NOT create apps outside `~/data/apps/`
- Do NOT use `x-positional` in manifest schema
- Do NOT create multiple tools per app — use one tool with action enum
