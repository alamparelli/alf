---
category: Development
tags: marketplace, apps, publish, developer, tools, SDK
order: 15
---

# Building Marketplace Apps

How to build, structure, and publish apps for the ALF marketplace.

## App structure

A marketplace app lives in `~/data/apps/<slug>/` and contains:

```
my-app/
  manifest.json    # Required — metadata, tool declarations
  app.json         # Optional — web UI display (name, icon, description)
  index.html       # Optional — web UI (served at /apps/<slug>)
  bin/
    <slug>         # Go binary (built for target arch)
  data/            # Runtime data (created by app, preserved across updates/uninstalls)
```

The `bin/<slug>` binary is the tool backend. The `index.html` is the Control Center web UI. An app can have both, either, or neither (metadata-only).

## manifest.json

Defines the app identity and its tool declarations. This is what the marketplace registry and the local installer both consume.

```json
{
  "name": "Journal",
  "slug": "journal",
  "version": "0.2.0",
  "description": "Personal journaling with mood tracking and reflection",
  "author": "Alf",
  "category": "productivity",
  "icon": "book-open",
  "tools": [
    {
      "name": "journal",
      "description": "Personal journal: write, read, search, reflect on, or delete entries.",
      "action": "journal",
      "parameters": {
        "type": "object",
        "properties": {
          "action": {
            "type": "string",
            "enum": ["write", "read", "search", "reflect", "delete"],
            "description": "Action to perform"
          },
          "content": { "type": "string", "description": "Journal entry text (write)" },
          "id": { "type": "string", "description": "Entry ID (read one, delete)" },
          "query": { "type": "string", "description": "Search query (search)" },
          "limit": { "type": "integer", "description": "Number of entries (read, default 10)" }
        },
        "required": ["action"]
      }
    }
  ]
}
```

**Fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Human-readable display name |
| `slug` | Yes | URL-safe identifier, matches directory name |
| `version` | Yes | Semver string |
| `description` | Yes | One-line summary |
| `author` | Yes | Developer or org name |
| `category` | No | e.g. `productivity`, `utilities`, `dev-tools` |
| `icon` | No | [Lucide](https://lucide.dev) icon name |
| `tools` | No | Array of tool declarations (see below) |

### Tool declarations

Each entry in `tools[]` becomes an ALF tool when the app is **enabled**. The manager creates a symlink in `~/data/tools/<tool-name>` pointing to the app binary, plus a `<tool-name>.json` schema file.

- `name` — tool name visible to ALF (must be unique across all apps)
- `description` — what the tool does (shown to the LLM)
- `action` — maps to an action handler in the binary
- `parameters` — JSON Schema for the tool input

**No `x-positional`**: SDK tools receive JSON on stdin, not positional CLI args. The `x-positional` convention is for shell-script tools only. Do not use it in marketplace manifests.

## app.json

Controls how the app appears in the Control Center sidebar. Only relevant if the app has a web UI (`index.html`).

```json
{
  "name": "Journal",
  "icon": "book-open",
  "description": "Personal journaling with mood tracking and reflection"
}
```

- `name` — sidebar display name (falls back to directory name)
- `icon` — Lucide icon name
- `description` — tooltip/subtitle text

## Go SDK (`pkg/appsdk`)

The SDK handles stdin JSON parsing, action dispatch, and response formatting.

### Minimal example

```go
package main

import "github.com/alamparelli/alf/pkg/appsdk"

func main() {
    app := appsdk.New("my-app")

    app.Action("greet", func(ctx *appsdk.Context) error {
        name := ctx.String("name")
        if name == "" {
            name = "world"
        }
        appsdk.Respond("Hello, " + name)
        return nil
    })

    app.Run()
}
```

### API reference

**`appsdk.New(name string) *App`** — creates an app. Reads `ALF_APP_DATA_DIR` env var for persistent storage path.

**`app.Action(name string, fn ActionFunc)`** — registers a handler for the named action.

**`app.Run()`** — reads JSON from stdin, resolves the action, and dispatches. Action resolution:
1. Binary name: if the binary is named `myapp-dosomething`, action = `dosomething`
2. Fallback: reads the `"action"` field from stdin JSON

If invoked with `--help`, prints available actions and exits (used by ALF's tool discovery).

**`ctx.String(key string) string`** — returns string arg or `""`.

**`ctx.Int(key string, def int) int`** — returns integer arg or `def`. Handles both JSON numbers and string-encoded integers.

**`ctx.DataDir`** — persistent data directory path (from `ALF_APP_DATA_DIR`). Survives app updates and uninstalls.

**`ctx.Args`** — raw `map[string]any` of all input fields.

**`appsdk.Respond(text string)`** — writes plain text to stdout (success response to ALF).

**`appsdk.RespondJSON(v any)`** — marshals and writes JSON to stdout.

**`appsdk.Fail(msg string)`** — writes to stderr and exits 1 (error response to ALF).

### Building

Cross-compile for the container architecture (typically `linux/amd64`):

```bash
GOOS=linux GOARCH=amd64 go build -o bin/my-app ./cmd/my-app/
```

Place the binary at `apps/<slug>/bin/<slug>` in the app directory.

## Web UI conventions

### Theme integration

Load the CC theme stylesheet and init script. Use CSS variables for all colors:

```html
<link rel="stylesheet" id="alf-theme-link" href="/static/theme-sage.css">
<script src="/static/theme-init.js"></script>
<style>
  body { background: var(--bg); color: var(--text); }
  .card { background: var(--bg-card); border: 1px solid var(--border); }
  h1 { color: var(--accent); }
  .muted { color: var(--text-dim); }
</style>
```

| Variable | Purpose |
|----------|---------|
| `var(--bg)` | Page background |
| `var(--text)` | Main text |
| `var(--text-dim)` | Secondary/muted text |
| `var(--accent)` | Brand color, links |
| `var(--on-accent)` | Text on accent background |
| `var(--border)` | Borders and dividers |
| `var(--bg-card)` | Card/panel background |

### Security (CSP)

- No external scripts (`<script src="https://...">` blocked)
- No inline event handlers (`onclick="..."` blocked) — use `addEventListener()`
- No external stylesheets — all CSS in `<style>` blocks
- Fetch/XHR restricted to same origin (`/api/*` works)

### Calling tools from the web UI

Use the `/api/bash` endpoint to invoke app binaries:

```javascript
function bash(cmd) {
  return fetch('/api/bash', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Requested-With': 'XMLHttpRequest'
    },
    credentials: 'same-origin',
    body: JSON.stringify({ command: cmd })
  }).then(r => r.json());
}

function appCmd(action, args) {
  var payload = Object.assign({ action: action }, args || {});
  var input = JSON.stringify(payload);
  var cmd = "echo '" + input.replace(/'/g, "'\\''") + "' | ALF_APP_DATA_DIR=/home/alf/data/apps/my-app/data /home/alf/data/apps/my-app/bin/my-app";
  return bash(cmd).then(res => {
    if (res.exit_code !== 0) throw new Error(res.error || res.output);
    return res.output || '';
  });
}
```

The pattern: pipe JSON into the binary via stdin, setting `ALF_APP_DATA_DIR` so the SDK can find its data directory.

## Publishing

Publishing uploads your app to the ALF marketplace registry (`POST /api/apps/{slug}/publish`). The registry authenticates via bearer token.

### Setup

1. Store your marketplace API key in the vault under service name `marketplace`:
   - Service: `marketplace`
   - Token type: Bearer token
   - The vault proxies authenticated requests to the registry

2. The Developer app (or a custom script) calls the registry's publish endpoint with:
   - `manifest` form field — the manifest JSON
   - `binary_amd64` / `binary_arm64` — compiled binaries (optional, per-arch)
   - `web_index.html`, `web_app.json` — web assets (prefixed with `web_`)

### Registry API

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/health` | GET | Bearer | Verify API key, returns developer name |
| `/api/catalog` | GET | Instance or Bearer | List all published apps |
| `/api/apps/{slug}/manifest` | GET | Instance or Bearer | Get app manifest |
| `/api/apps/{slug}/download?arch=amd64` | GET | Instance or Bearer | Download binary |
| `/api/apps/{slug}/web/{file}` | GET | Instance or Bearer | Download web asset |
| `/api/apps/{slug}/publish` | POST | Bearer | Publish/update an app |

Instance requests use the `X-Alf-Instance: true` header (set automatically by the local manager).

## Lifecycle states

| State | Meaning |
|-------|---------|
| *available* | Listed in the remote catalog, not installed locally |
| `installed` | Downloaded to `~/data/apps/<slug>/`, tools not yet active |
| `enabled` | Tool symlinks + JSON schemas created in `~/data/tools/` — tools are live |
| `disabled` | Installed but symlinks removed — tools inactive, data preserved |

State transitions:

- **Install**: downloads manifest + binary + web assets from registry. State = `installed`.
- **Enable**: creates symlinks `tools/<tool-name> -> ../apps/<slug>/bin/<slug>` and writes `tools/<tool-name>.json` schema. State = `enabled`.
- **Disable**: removes symlinks and schema files. State = `disabled`. Data preserved.
- **Uninstall**: removes everything except `data/`. User data is never deleted.
- **Update**: re-downloads from registry, preserves `data/` and current state. If enabled, re-creates symlinks.

State is persisted in `~/data/apps/.state.json`.

## Example: the journal app

The bundled journal app demonstrates the full pattern.

**Backend** (`cmd/journal/main.go`):
- Uses `appsdk.New("journal")` with five actions: `write`, `read`, `search`, `reflect`, `delete`
- Stores entries as JSON in `ctx.DataDir/entries.json`
- Returns plain text via `appsdk.Respond()`

**Manifest** (`manifest.json`):
- Declares one tool `journal` with an `action` enum and per-action parameters
- All parameters use standard JSON Schema — no `x-positional`

**Web UI** (`index.html`):
- Tabbed interface (Write / Entries / Search / Reflect)
- Calls the binary via `/api/bash` with JSON piped to stdin
- Uses `var(--bg)`, `var(--accent)`, etc. for theme integration
- No external scripts or stylesheets — fully self-contained
