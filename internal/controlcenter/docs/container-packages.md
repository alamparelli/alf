---
category: Development
tags: tools, apps, config, packages, persistence, setup, docker, build
order: 10
---

# Building Tools & Extensions

How to create tools, apps, and extend ALF's capabilities inside the container.

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
imagemagick
pandoc
```

Edit via: Workspace Explorer > `config.d/packages.txt`, then `alf restart`.

### User bootstrap (`data/bootstrap.sh`)

Runs as the `alf` user (not root) at every startup. Use for pip/npm installs and starting services.

```bash
#!/bin/bash
set -e

# Python
pip3 install --quiet --break-system-packages requests numpy

# Node.js
npm install -g --silent typescript

# Start a background service
nohup python3 ~/data/tools/my-api serve &
```

> **Do not** put `apt install` commands in bootstrap.sh - it runs as a non-root user. Use `config.d/packages.txt` instead.

### Rules

1. Use quiet/non-interactive flags (`--quiet`, `-y`, `-qq`, `--silent`)
2. Append new lines - do not overwrite existing content
3. bootstrap.sh runs as user `alf` - no `sudo`, no `apt`
4. If bootstrap fails, the daemon still starts (warnings logged)

## What survives a rebuild

| Survives | Lost |
|----------|------|
| Everything in `~/data/` | pip/apt/npm packages (reinstalled automatically) |
| `config.d/packages.txt` | Binaries in `/usr/local/bin` |
| Scripts in `~/data/tools/` | System-level config changes |
| Apps in `~/data/apps/` | Anything outside volumes |
| Skills in `~/data/skills/` | |
| `~/data/bootstrap.sh` | |

**Rule of thumb:** system packages go in `config.d/packages.txt`, pip/npm go in `data/bootstrap.sh`, custom scripts go in `data/tools/`.

## What's next?

- [Creating Skills](docs:creating-skills) - full guide on skill creation
- [Tools Reference](docs:tools-reference) - built-in CLI tools
