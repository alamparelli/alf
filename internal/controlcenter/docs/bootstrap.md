---
category: Configuration
tags: bootstrap, setup, packages, install, startup
order: 6
---

# Bootstrap & Packages

How ALF installs packages and runs startup scripts.

## Two-phase startup

ALF's startup has two phases, each with a different purpose and privilege level:

| Phase | File | Runs as | Purpose |
|-------|------|---------|---------|
| 1. System packages | `config.d/packages.txt` | root | `apt install` of system packages |
| 2. User bootstrap | `data/bootstrap.sh` | alf (uid 1000) | pip/npm install, start services, config |

### Phase 1: System packages (`config.d/packages.txt`)

Add one Debian package name per line. Packages are installed as root at container startup, only when the file changes.

```
graphviz
texlive-base
chromium
```

**Where to edit:** Workspace Explorer > `config.d/packages.txt`

Packages listed here survive container restarts. On `alf upgrade` (new image), the file persists in `config.d/` and packages are reinstalled automatically on first boot.

> ALF will suggest adding packages here when it detects a missing command. For example: *"Command `jq` not found. Add `jq` to `config.d/packages.txt` and restart."*

### Phase 2: User bootstrap (`data/bootstrap.sh`)

Runs as the `alf` user (not root). Use it for:

- **pip/npm installs** (user-space, no sudo)
- **Starting background services** (API servers, daemons)
- **File permissions** (chmod on secrets)
- **Symlinks and config** within the data directory

```bash
#!/usr/bin/env bash
set -e

# Python packages
pip3 install --quiet --break-system-packages requests numpy

# Node.js packages
npm install -g --silent typescript

# Start a background service
nohup python3 /home/alf/data/tools/my-api serve &
```

**What bootstrap.sh cannot do** (no root):
- `apt install` -- use `config.d/packages.txt` instead
- Write to `/usr/`, `/etc/`, `/root/`
- Install system-wide binaries

## Where to edit

| File | Via Workspace Explorer | Via SSH |
|------|----------------------|---------|
| `config.d/packages.txt` | Home > Workspace > `config.d/packages.txt` | `~/alf/config.d/packages.txt` |
| `data/bootstrap.sh` | Home > Workspace > `bootstrap.sh` | `~/alf/data/bootstrap.sh` |

After editing either file, run `alf restart` to apply changes.

## What survives a rebuild?

When you run `alf upgrade`, the container image is rebuilt. Everything outside volumes is lost.

| Survives rebuild | Lost on rebuild |
|-----------------|----------------|
| `config.d/packages.txt` | Installed apt packages (reinstalled automatically) |
| `data/bootstrap.sh` | Installed pip/npm packages (reinstalled automatically) |
| Everything in `data/` | Binaries in `/usr/local/bin` |
| Config in `config.d/` | System-level config changes |

**Rule of thumb:** system packages go in `packages.txt`, everything else goes in `bootstrap.sh`.

## Best practices

| Do | Don't |
|----|-------|
| Use quiet flags (`--quiet`, `-y`, `-qq`) | Write interactive prompts in bootstrap.sh |
| Put apt packages in `packages.txt` | Run `apt install` in bootstrap.sh |
| Use `set -e` in bootstrap.sh | Ignore failures silently |
| Keep installs idempotent | Assume previous state |

## Pre-installed packages

These are already in the container image - no need to add them to `packages.txt`.

| Category | Packages |
|----------|----------|
| **Shell & editors** | `bash` `nano` `less` `tmux` |
| **Network & transfer** | `curl` `wget` `openssh-client` `rsync` `dnsutils` `net-tools` |
| **Search & files** | `git` `ripgrep` `tree` `file` `trash-cli` `unzip` `zip` |
| **Build tools** | `build-essential` (gcc, g++, make) |
| **Data & docs** | `jq` `sqlite3` `pandoc` `poppler-utils` |
| **Media** | `ffmpeg` `imagemagick` |
| **System** | `ca-certificates` `xz-utils` `htop` |
| **Dev tools** | `gh` (GitHub CLI) `python3` `pip3` (amd64) `node` `npm` |

Need something else? Add it to `config.d/packages.txt` (one package per line) and restart. Packages are installed as root on startup and persist across restarts.

## What's next?

- [Container Packages](docs:container-packages) -- detailed guide on tools, apps, and extensions
- [Getting Started](docs:getting-started) -- ALF setup and overview
