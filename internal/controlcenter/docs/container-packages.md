---
category: Development
tags: tools, pages, config, packages, persistence, setup, docker, build
order: 10
---

# Building Tools & Extensions

How to create tools, pages, and extend ALF's capabilities inside the container.

## Directory Structure

ALF runs in a Docker container. The `data/` volume is persistent across restarts but **not across container rebuilds** (image updates).

| Directory | Persistent | Purpose |
|-----------|-----------|---------|
| `~/data/tools.d/` | Yes (volume) | Symlinks to system tools — auto-generated, do not edit |
| `~/data/tools/` | Yes (volume) | Custom tool definitions (JSON) |
| `~/data/pages/` | Yes (volume) | HTML dashboards served at `/pages/<name>` |
| `~/data/skills/` | Yes (volume) | Custom skill definitions (SKILL.md) |
| `~/data/context/` | Yes (volume) | Context files injected into every conversation |
| `~/data/config.d/` | Read-only mount | Configuration (tiers.json, config.json, agents/) |
| `~/data/skills.d/` | Read-only mount | Bundled skills (read-only copy) |

## Creating a CLI Tool

Write a script, make it executable, and place it in a directory that's in PATH (`/opt/alf/tools/` is in PATH).

```bash
# Example: create a tool in the persistent data volume
cat > ~/data/tools/my-tool << 'SCRIPT'
#!/bin/bash
echo "Hello from my-tool"
echo "Args: $@"
SCRIPT
chmod +x ~/data/tools/my-tool
```

To make it available as a named command, create a symlink in tools.d/:
```bash
ln -sf ~/data/tools/my-tool ~/data/tools.d/my-tool
```

**Limitation:** `tools.d/` symlinks are regenerated from `/opt/alf/tools/` on each daemon restart. Custom symlinks in `tools.d/` will be removed. Place custom executables directly in `~/data/tools/` and call them by full path, or add `~/data/tools` to your script's PATH.

## Creating an HTML Page

Write an HTML file to `~/data/pages/`. It becomes accessible at `https://<domain>/pages/<name>` in the Control Center.

```bash
cat > ~/data/pages/status.html << 'HTML'
<html>
<body>
  <h1>System Status</h1>
  <pre id="out"></pre>
  <script>
    fetch('/api/bash', {method:'POST', headers:{'Content-Type':'application/json'}, body:'{"command":"uptime"}'})
      .then(r=>r.json()).then(d=>document.getElementById('out').textContent=d.output);
  </script>
</body>
</html>
HTML
```

**Limitations:**
- Pages run inside a strict Content Security Policy (no external scripts, no inline event handlers)
- Maximum file size: 5MB
- File names must be alphanumeric + hyphens only

## Package Persistence Across Rebuilds

When the container image is rebuilt (`alf upgrade`), everything outside the `data/` volume is lost. This includes pip packages, apt packages, npm packages, and any binaries installed at runtime.

### setup.sh

The file `~/data/setup.sh` is automatically executed at daemon startup. It only re-runs when its content changes (hash-checked for fast boot).

**When you install any package, always append the install command to `~/data/setup.sh`:**

```bash
# Append to setup.sh — do NOT overwrite
cat >> ~/data/setup.sh << 'EOF'
pip3 install --quiet requests
EOF
```

### Example setup.sh

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

## What Survives a Rebuild

| Survives | Lost |
|----------|------|
| Everything in `~/data/` | pip/apt/npm packages |
| Scripts in `~/data/tools/` | Binaries in `/usr/local/bin` |
| HTML in `~/data/pages/` | System-level config changes |
| `~/data/setup.sh` | Anything outside data volume |

**Rule of thumb:** if you create it, put it in `~/data/`. If you install it, register it in `~/data/setup.sh`.
