# CLI Tool (appsdk) -- Reference

Use this architecture when the LLM needs to create, read, update, or delete data (todo items, journal entries, bookmarks, etc.).

## Directory structure

```
apps/<slug>/
  manifest.json      # metadata + tool schema
  app.json           # REQUIRED if web UI
  index.html         # OPTIONAL web UI
  main.go            # Go source (appsdk)
  go.mod
  data/
    <slug>.db        # SQLite (created at runtime)
```

At install time, ALF compiles `main.go` and places the binary in `~/data/tools/<slug>`. Never compile yourself.

## manifest.json -- tool schema

One tool per app, `action` enum for sub-commands:

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
      "description": "Describe what each action does",
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
- **One tool with action enum** -- not separate tools per action
- **`required: ["action"]`** -- always
- **`x-positional`** -- fields that become positional CLI args (in order); rest become `--key value` flags

## main.go template

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
    // ctx.DataDir = $ALF_APP_DATA_DIR (persistent storage)
    appsdk.Respond(fmt.Sprintf("Created: %s", name))
    return nil
}

func actionList(ctx *appsdk.Context) error {
    // appsdk.RespondJSON(v) for structured output
    appsdk.RespondJSON([]string{})
    return nil
}

func actionDelete(ctx *appsdk.Context) error {
    id := ctx.String("id")
    if id == "" {
        return fmt.Errorf("id is required")
    }
    appsdk.Respond("Deleted: " + id)
    return nil
}
```

## appsdk API

| Method | Description |
|---|---|
| `ctx.String("key")` | String arg or `""` |
| `ctx.Int("key", default)` | Int arg with fallback |
| `ctx.DataDir` | Persistent storage path (`$ALF_APP_DATA_DIR`) |
| `appsdk.Respond(text)` | Text output to stdout |
| `appsdk.RespondJSON(v)` | JSON output to stdout |
| `appsdk.Fail(msg)` | Error to stderr + exit 1 |

## go.mod

```
module my-app

go 1.21

require github.com/alamparelli/alf v0.0.0
```

## SQLite pattern (inside an action)

```go
import (
    "database/sql"
    "path/filepath"
    _ "modernc.org/sqlite"
)

func getDB(ctx *appsdk.Context) (*sql.DB, error) {
    dbPath := filepath.Join(ctx.DataDir, "my-app.db")
    db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
    if err != nil { return nil, err }
    db.SetMaxOpenConns(1)
    db.Exec(`CREATE TABLE IF NOT EXISTS items (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
        created_at TEXT DEFAULT (datetime('now'))
    )`)
    return db, nil
}
```

## Frontend for CLI tool apps

If the app has a web UI, the frontend uses `AlfSDK.tool()` to call the CLI:

```js
AlfSDK.tool('list').then(function(out) {
    try { items = JSON.parse(out); } catch(e) { items = []; }
    render();
});

AlfSDK.tool('create', { name: 'New item' }).then(function() { load(); });
```

Read `FRONTEND.md` for the full frontend template and AlfSDK reference.
