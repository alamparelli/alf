---
category: Development
tags: tools, apps, config, packages, persistence, setup, docker, build
order: 10
---

# Building Tools & Extensions

How to create tools, apps, and extend ALF's capabilities. ALF has three ways to package executable logic — pick the right one before you start, or you'll fight the system later.

## Pick the right kind

| Kind | Who authors | Best for | Lives in |
|---|---|---|---|
| **bash / Python tool** | Maintainer (ships with the daemon, TCB access) | Quick glue scripts, file helpers, wrappers over CLI binaries | `~/data/tools/<name>` |
| **Go-kind app** | Maintainer (compiled at install) | App shells with frontend + backend, marketplace apps | `~/data/apps/<slug>/` |
| **WASM-kind tool/app** | Anyone — third-party, LLM-authored, untrusted | Isolated tools, anything ALF writes for you, anything imported from a marketplace | `~/data/skills.d/wasm/<id>/` |

**Decision rule** — when in doubt, pick **WASM-kind**. The 0.8.0 architecture mandates it for any non-maintainer capability. Bash/Python tools are reserved for code shipped as part of the daemon distribution.

**Quick tree:**

1. **ALF is writing this for you?** → **WASM-kind**. Use the `wasm-builder` skill — see [Creating WASM Tools](docs:wasm-tools).
2. **You're hand-writing a maintainer utility (TCB script)?** → **bash / Python**. Read on.
3. **You need a full app with a UI tab?** → **Go-kind**. See [Building Marketplace Apps](docs:marketplace-apps).
4. **Anything else (third-party, untrusted, marketplace-bound)?** → **WASM-kind**.

All three kinds run inside ALF's isolation model — signed bundles, declared permissions, no ambient access beyond what the manifest grants. See [Isolation Model](docs:isolation-model) for the full mental model.

The rest of this page covers **bash / Python tools** and **simple HTML apps**. For WASM tools, see [Creating WASM Tools](docs:wasm-tools). For full marketplace apps, see [Building Marketplace Apps](docs:marketplace-apps).

## Directory structure

ALF runs in a Docker container. The `data/` volume is persistent across restarts but **not across container rebuilds** (image updates).

| Directory | Persistent | Purpose |
|-----------|-----------|---------|
| `~/data/tools/` | Yes (volume) | Custom scripts and executables |
| `~/data/apps/` | Yes (volume) | App directories served at `/apps/<name>` |
| `~/data/skills/` | Yes (volume) | Custom skills (SKILL.md folders) |
| `~/data/context/` | Yes (volume) | Context files injected into every conversation |
| `~/data/tools.d/` | Yes (volume) | Symlinks to system tools - auto-generated, do not edit |
| `~/data/skills.d/` | Read-only mount | Bundled skills (read-only copy) |
| `~/data/config.d/` | Read-only mount | Configuration (tiers.json, config.json, agents/) |

## Creating a CLI tool

Write a script, make it executable, and place it in `~/data/tools/`.

```bash
cat > ~/data/tools/my-tool << 'SCRIPT'
#!/bin/bash
echo "Hello from my-tool"
echo "Args: $@"
SCRIPT
chmod +x ~/data/tools/my-tool
```

Your tool is now available immediately - both `~/data/tools/` and `~/data/tools.d/` are in ALF's PATH, so you can call it by name:

```bash
my-tool --help
```

No full path needed.

### Making tools discoverable

Add a `--help` flag so ALF knows what your tool does:

```bash
cat > ~/data/tools/disk-check << 'SCRIPT'
#!/bin/bash
if [ "$1" = "--help" ]; then
    echo "Check disk usage across all mounted volumes."
    echo "Usage: disk-check [--threshold N]"
    echo "  --threshold N   Alert if any volume exceeds N% (default: 80)"
    exit 0
fi

threshold=${2:-80}
df -h | awk -v t="$threshold" 'NR>1 && int($5)>t {print "WARNING:", $6, "is", $5, "full"}'
SCRIPT
chmod +x ~/data/tools/disk-check
```

> `tools.d/` symlinks are regenerated from `/opt/alf/tools/` on each daemon restart. Custom symlinks you place there will be removed. Put your tools in `~/data/tools/` instead.

## Creating an app

Create a directory in `~/data/apps/` with an `index.html` file. It becomes accessible at `https://<domain>/apps/<name>` in the Control Center sidebar. Add an optional `app.json` for metadata (display name, Lucide icon, description).

```bash
mkdir -p ~/data/apps/status
cat > ~/data/apps/status/index.html << 'HTML'
<html>
<head>
<style>
  body { font-family: sans-serif; padding: 20px; background: var(--bg); color: var(--text); }
  .card { background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px; padding: 16px; }
  h1 { color: var(--accent); }
</style>
</head>
<body>
  <h1>System Status</h1>
  <div class="card">
    <pre id="out"></pre>
  </div>
  <script>
    fetch('/api/bash', {method:'POST', headers:{'Content-Type':'application/json', 'X-Requested-With':'app'}, body:'{"command":"uptime && df -h"}'})
      .then(r=>r.json()).then(d=>document.getElementById('out').textContent=d.output);
  </script>
</body>
</html>
HTML
```

### Apps with background services

If your app needs a backend process (API server, worker, etc.), add a `service.json` next to the `app.json`. The daemon supervises it automatically — restart on crash, restart on daemon reboot.

```bash
mkdir -p ~/data/apps/my-api
cat > ~/data/apps/my-api/service.json << 'JSON'
{
  "name": "My API",
  "command": "./server",
  "args": ["--port", "8764"],
  "env": {"PORT": "8764"},
  "restart": "always",
  "restart_delay": "3s",
  "max_restarts": 10,
  "enabled": true
}
JSON
```

| Field | Description | Default |
|-------|-------------|---------|
| `command` | Executable to run (relative to app dir, must stay within the app directory) | Required |
| `args` | Command-line arguments | `[]` |
| `env` | Environment variables | `{}` |
| `restart` | `"always"`, `"on-failure"`, or `"no"` | `"always"` |
| `restart_delay` | Base delay between restarts (exponential backoff up to 60s) | `"3s"` |
| `max_restarts` | Maximum restart attempts before giving up | `100` |
| `enabled` | Whether to start the service | `true` |

The service runs under the same user as the daemon. Logs appear in the daemon log prefixed with `[app-slug]`.

**Lifecycle:**
- Daemon start → all enabled services start
- Service crash → automatic restart with exponential backoff (resets after 5min stable)
- Daemon stop → SIGTERM → 5s grace → SIGKILL
- Edit `service.json` → restart daemon to pick up changes

### App rules

**Security (Content Security Policy):**
- No external scripts - `<script src="https://...">` is blocked
- No inline event handlers - `onclick="..."` is blocked. Use `addEventListener()` instead
- No external stylesheets - `<link rel="stylesheet" href="https://...">` is blocked
- Fetch/XHR is restricted to same origin (`/api/*` endpoints work fine)

**CSS:**
- All CSS must be in `<style>` blocks (inline) - external stylesheets are blocked
- Use CC theme variables to match the current theme automatically:

| Variable | What it styles |
|----------|---------------|
| `var(--bg)` | Page background |
| `var(--text)` | Main text color |
| `var(--text-dim)` | Secondary/muted text |
| `var(--accent)` | Brand color (links, highlights) |
| `var(--border)` | Border and divider color |
| `var(--bg-card)` | Card/panel background |

- Avoid using the `.empty` class name - it conflicts with CC internals

**Other limits:**
- Maximum file size: 5MB
- File names must be alphanumeric + hyphens only

## Creating a skill

Skills are instructions that ALF follows for specific topics. See [Creating Skills](docs:creating-skills) for a full guide.

Quick version:

```bash
mkdir -p ~/data/skills/my-skill
cat > ~/data/skills/my-skill/SKILL.md << 'EOF'
---
name: my-skill
description: What this skill does (one line)
triggers: keyword1, keyword2, keyword3
---

Your instructions here. ALF follows these when a trigger matches.
EOF
```

## Package persistence across rebuilds

When the container image is rebuilt (`alf upgrade`), everything outside the volumes is lost. This includes pip packages, apt packages, npm packages, and any binaries installed at runtime.

ALF uses a two-phase startup to reinstall everything automatically:

### System packages (`config.d/packages.txt`)

Add one Debian package name per line. Installed as root at startup, only when the file changes.

```
jq
ffmpeg
```

Edit via: Workspace Explorer > `config.d/packages.txt`, then `alf restart`.

### User packages (pip, npm)

Install packages directly via ALF's bash tool. Results persist in cache volumes across restarts:

```bash
pip3 install --quiet --break-system-packages requests numpy
npm install -g --silent typescript
```

No need for a bootstrap script — cache volumes (`~/.local`, `~/.npm`, `~/.cache`) survive restarts. Packages are only lost on image rebuild (`alf upgrade`), in which case ALF can reinstall them when needed.

### Background services

Use `service.json` in app directories instead of `nohup ... &`. See [Apps with background services](#apps-with-background-services) above.

> **Legacy:** `data/bootstrap.sh` is deprecated. It still runs once at container start for backward compatibility, but new setups should use `config.d/packages.txt` for system packages, pip/npm for user packages (cached in volumes), and `service.json` for background services.

## What survives a rebuild

| Survives | Lost on rebuild |
|----------|------|
| Everything in `~/data/` | apt packages (reinstalled via `packages.txt`) |
| `config.d/packages.txt` | Binaries in `/usr/local/bin` |
| Scripts in `~/data/tools/` | System-level config changes |
| Apps + services in `~/data/apps/` | Anything outside volumes |
| Skills in `~/data/skills/` | |
| pip/npm packages (in cache volumes) | |

**Rule of thumb:** system packages go in `config.d/packages.txt`, pip/npm install directly (cached in volumes), background services go in `apps/*/service.json`.

## What's next?

- [Isolation Model](docs:isolation-model) — the 3 layers, trust, and the kind decision tree
- [Creating WASM Tools](docs:wasm-tools) — for tools ALF writes for you or that you import
- [Creating Skills](docs:creating-skills) — full guide on skill creation
- [Tools Reference](docs:tools-reference) — built-in CLI tools
