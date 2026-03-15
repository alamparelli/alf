---
category: Configuration
tags: bootstrap, setup, packages, install, startup
order: 6
---

# Packages & Startup

How to install extra software and configure ALF's container.

## Adding system packages

Need a tool that isn't pre-installed? Add it to `config.d/packages.txt` — one package name per line:

```
graphviz
chromium
texlive-base
```

**How to edit:** Go to **Home > Workspace > `config.d/packages.txt`**, add your packages, then run `alf restart`.

Packages are installed automatically at startup. After an `alf upgrade`, they're reinstalled from the same file.

> ALF suggests adding packages when it detects a missing command. For example: *"Command `jq` not found. Add `jq` to `config.d/packages.txt` and restart."*

## Installing Python/Node packages

Install packages directly — they persist in cache volumes across restarts:

```bash
pip3 install --quiet --break-system-packages requests numpy
npm install -g --silent typescript
```

Packages are only lost on image rebuild (`alf upgrade`), in which case ALF can reinstall them when needed.

## Background services

If your app needs a backend process, use `service.json` in an app directory. The daemon supervises it automatically. See [Building Tools & Extensions](docs:container-packages) for details.

## Pre-installed packages

These are already in the container — no need to add them:

| Category | Packages |
|----------|----------|
| **Shell & editors** | `bash` `nano` `less` `tmux` |
| **Network** | `curl` `wget` `openssh-client` `rsync` `dnsutils` `net-tools` |
| **Search & files** | `git` `ripgrep` `tree` `file` `trash-cli` `unzip` `zip` |
| **Build tools** | `build-essential` (gcc, g++, make) |
| **Data & docs** | `jq` `sqlite3` `pandoc` `poppler-utils` |
| **Media** | `ffmpeg` `imagemagick` |
| **System** | `ca-certificates` `xz-utils` `htop` |
| **Dev tools** | `gh` (GitHub CLI) `python3` `pip3` (amd64) `node` `npm` |

## What survives a rebuild?

When you run `alf upgrade`, the container image is rebuilt. Everything outside volumes is lost.

| Survives | Lost on rebuild |
|----------|----------------|
| Everything in `data/` | apt packages (reinstalled via `packages.txt`) |
| `config.d/packages.txt` | Binaries in `/usr/local/bin` |
| pip/npm packages (in cache volumes) | System-level config changes |

> **Legacy note:** `data/bootstrap.sh` is deprecated. It still runs at startup for backward compatibility, but new setups should use `packages.txt` for system packages, pip/npm directly (cached in volumes), and `service.json` for background services.

## What's next?

- [Building Tools & Extensions](docs:container-packages) — create tools, apps, and custom scripts
- [Getting Started](docs:getting-started) — ALF setup and overview
