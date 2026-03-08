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
| `~/data/tools.d/` | Yes (volume) | Symlinks to system tools — auto-generated, do not edit |
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

Your tool is now available at `~/data/tools/my-tool`. Call it by full path, or add `~/data/tools` to your script's PATH.

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
    fetch('/api/bash', {method:'POST', headers:{'Content-Type':'application/json'}, body:'{"command":"uptime && df -h"}'})
      .then(r=>r.json()).then(d=>document.getElementById('out').textContent=d.output);
  </script>
</body>
</html>
HTML
```

### App rules

**Security (Content Security Policy):**
- No external scripts — `<script src="https://...">` is blocked
- No inline event handlers — `onclick="..."` is blocked. Use `addEventListener()` instead
- No external stylesheets — `<link rel="stylesheet" href="https://...">` is blocked
- Fetch/XHR is restricted to same origin (`/api/*` endpoints work fine)

**CSS:**
- All CSS must be in `<style>` blocks (inline) — external stylesheets are blocked
- Use CC theme variables to match the current theme automatically:

| Variable | What it styles |
|----------|---------------|
| `var(--bg)` | Page background |
| `var(--text)` | Main text color |
| `var(--text-dim)` | Secondary/muted text |
| `var(--accent)` | Brand color (links, highlights) |
| `var(--border)` | Border and divider color |
| `var(--bg-card)` | Card/panel background |

- Avoid using the `.empty` class name — it conflicts with CC internals

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

When the container image is rebuilt (`alf upgrade`), everything outside the `data/` volume is lost. This includes pip packages, apt packages, npm packages, and any binaries installed at runtime.

### bootstrap.sh

The file `~/data/bootstrap.sh` is automatically executed at daemon startup. It only re-runs when its content changes (hash-checked for fast boot).

**When you install any package, always append the install command to `~/data/bootstrap.sh`:**

```bash
# Append to bootstrap.sh — do NOT overwrite
cat >> ~/data/bootstrap.sh << 'EOF'
pip3 install --quiet requests
EOF
```

### Example bootstrap.sh

```bash
#!/bin/bash
set -e

# Python
pip3 install --quiet faster-whisper requests numpy

# System packages
apt-get update -qq && apt-get install -y --no-install-recommends jq htop

# Node.js
npm install -g --silent typescript

# Go
GOBIN=/usr/local/bin go install github.com/example/tool@latest

# Binary download
curl -fsSL https://example.com/tool.tar.gz | tar xz -C /usr/local/bin
```

### Rules

1. Use quiet/non-interactive flags (`--quiet`, `-y`, `-qq`, `--silent`)
2. Append new lines — do not overwrite existing content
3. Script runs as root — no `sudo` needed
4. If installation fails, it will retry on next restart (hash not saved on failure)

## What survives a rebuild

| Survives | Lost |
|----------|------|
| Everything in `~/data/` | pip/apt/npm packages |
| Scripts in `~/data/tools/` | Binaries in `/usr/local/bin` |
| Apps in `~/data/apps/` | System-level config changes |
| Skills in `~/data/skills/` | Anything outside data volume |
| `~/data/bootstrap.sh` | |

**Rule of thumb:** if you create it, put it in `~/data/`. If you install it, register it in `~/data/bootstrap.sh`.

## What's next?

- [Creating Skills](docs:creating-skills) — full guide on skill creation
- [Tools Reference](docs:tools-reference) — built-in CLI tools
