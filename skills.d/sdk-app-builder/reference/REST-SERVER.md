# REST Server -- Reference

Use this architecture for rich web UIs, games, dashboards, and complex apps where the user interacts directly.
Also read `AIG.md` for design system classes and `FRONTEND.md` for AlfSDK init.

## Directory structure

```
apps/<slug>/
  manifest.json      # metadata (no tools unless LLM needs access)
  app.json           # web UI metadata
  service.json       # daemon supervision
  index.html         # web UI (AlfSDK)
  main.go            # Go server source  (or server.py for Python)
  go.mod
  data/
    <slug>.db        # SQLite (created at runtime)
    port             # port written by server at startup
```

At install time, ALF compiles `main.go` into a `server` binary. Never compile yourself.

## service.json

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

For Python: `"command": "python3", "args": ["server.py"]` -- no compilation needed.

The daemon auto-supervises: restart on crash, start on boot, SIGTERM on shutdown.

## Server requirements

- Pick a free port and write it to `data/port`
- Use SQLite in `data/` for all persistent storage
- Expose JSON REST endpoints
- Listen on `127.0.0.1` only

## Sandbox

App servers run inside a chroot jail. The server process sees:

- System binaries (`/bin`, `/usr`, `/lib`, `/sbin`, `/lib64`) — read-only
- `/home/alf/data/apps/<slug>/` — full app directory (read-write)
- `/home/alf/data/tools/` — shared tool binaries (read-only)
- `/dev/{null,zero,urandom,random}`, `/proc`, private `/tmp`
- TLS CA certs and DNS resolution

The server does NOT see other apps, vault data, secrets, or `.claude/` config.

### Vault proxy (external API access)

If your app needs external APIs (OpenRouter, Google, etc.), declare services in `manifest.json`:
```json
{ "services": ["openrouter"] }
```

The daemon creates a vault proxy socket at `VAULT_PROXY_SOCK`. Use the SDK:
```go
import "github.com/alamparelli/alf/pkg/appsdk"

vc, err := appsdk.NewVaultClient()
resp, err := vc.Proxy("openrouter", "POST", "/v1/chat/completions", body)
// or
var result MyStruct
err = vc.ProxyJSON("openrouter", "POST", "/v1/chat/completions", reqBody, &result)
```

The proxy injects authentication server-side — your app never sees API keys. Only declared services are allowed; requests to undeclared services return 403.

**Do not** hardcode API URLs or tokens. Do not access paths outside your app directory.

### Restarting an app service

To restart an app's background service (e.g. after a config change or to recover from an error):

```
POST /api/apps/{slug}/restart → {"ok": true, "slug": "{slug}"}
```

This is available from the CC frontend, ALF via bash (`curl -X POST http://localhost:8080/api/apps/my-app/restart`), and through the tools proxy socket.

## Go server template (main.go)

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

func respond(w http.ResponseWriter, code int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    json.NewEncoder(w).Encode(v)
}

func listItems(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        rows, err := db.Query("SELECT id, title, created_at FROM items ORDER BY id DESC")
        if err != nil { respond(w, 500, map[string]string{"error": err.Error()}); return }
        defer rows.Close()
        var items []map[string]any
        for rows.Next() {
            var id int; var title, createdAt string
            rows.Scan(&id, &title, &createdAt)
            items = append(items, map[string]any{"id": id, "title": title, "created_at": createdAt})
        }
        if items == nil { items = []map[string]any{} }
        respond(w, 200, items)
    }
}

func createItem(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var body map[string]string
        json.NewDecoder(r.Body).Decode(&body)
        res, err := db.Exec("INSERT INTO items (title) VALUES (?)", body["title"])
        if err != nil { respond(w, 500, map[string]string{"error": err.Error()}); return }
        id, _ := res.LastInsertId()
        respond(w, 201, map[string]any{"id": id})
    }
}

func deleteItem(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id := chi.URLParam(r, "id")
        db.Exec("DELETE FROM items WHERE id = ?", id)
        respond(w, 200, map[string]string{"ok": "deleted"})
    }
}
```

## go.mod

```
module SLUG

go 1.21

require (
    github.com/go-chi/chi/v5 v5.0.12
    modernc.org/sqlite v1.29.9
)
```

## Python server template (server.py)

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
        else:
            self._respond(404, {"error": "not found"})
    def do_POST(self):
        if self.path == "/api/SLUG":
            body = self._read_body()
            with get_db() as conn:
                cur = conn.execute("INSERT INTO items (title) VALUES (?)", (body.get("title", ""),))
            self._respond(201, {"id": cur.lastrowid})
        else:
            self._respond(404, {"error": "not found"})
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

## Frontend for REST server apps

The frontend fetches directly from the local server via the ALF proxy path `/apps/SLUG/api/...`:

```js
AlfSDK.api('/apps/SLUG/api/items').then(function(data) {
    items = data;
    render();
});
```

Read `FRONTEND.md` for the full frontend template and AlfSDK reference.
