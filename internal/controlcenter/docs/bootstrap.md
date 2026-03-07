---
category: Configuration
tags: bootstrap, setup, packages, install, startup
order: 6
---

# Bootstrap Script

Automatically install packages and configure the container at every startup.

## What is bootstrap.sh?

The file `data/bootstrap.sh` runs automatically when the ALF daemon starts. Use it to install packages, download binaries, or configure tools that don't survive a container rebuild.

ALF creates an empty `bootstrap.sh` during `alf init`. You fill it with your commands.

## How it works

1. Daemon starts and checks if `data/bootstrap.sh` exists
2. Computes a SHA-256 hash of the file content
3. If the hash matches the previous run, **skips execution** (fast boot)
4. If the content changed, runs the script and saves the new hash
5. If the script fails, the hash is **not saved** so it retries on next restart

This means:
- First boot after editing: script runs
- Subsequent boots with no changes: script is skipped instantly
- Failed runs: automatically retry on next restart

## Example

```bash
#!/usr/bin/env bash
set -e

# System packages
apt-get update -qq && apt-get install -y --no-install-recommends jq htop

# Python
pip3 install --quiet faster-whisper requests numpy

# Node.js
npm install -g --silent typescript

# Binary download
curl -fsSL https://example.com/tool.tar.gz | tar xz -C /usr/local/bin
```

## Best practices

| Do | Don't |
|----|-------|
| Use quiet flags (`--quiet`, `-y`, `-qq`) | Write interactive prompts |
| Append new commands at the bottom | Delete existing working lines |
| Use `set -e` to stop on errors | Ignore failures silently |
| Keep installs idempotent | Assume previous state |

## Where to edit

- **Workspace Explorer:** Home > Workspace > `data/bootstrap.sh`
- **Telegram:** Ask ALF to edit the file (requires a write-capable tier)
- **SSH:** Edit directly at `~/alf/data/bootstrap.sh` on your host

## What survives a rebuild?

When you run `alf upgrade`, the container image is rebuilt. Everything outside the `data/` volume is lost, including pip/apt/npm packages and binaries in `/usr/local/bin`.

That's why `bootstrap.sh` exists: it reinstalls everything automatically.

| Survives rebuild | Lost on rebuild |
|-----------------|----------------|
| Everything in `data/` | pip/apt/npm packages |
| `data/bootstrap.sh` | Binaries in `/usr/local/bin` |
| Scripts in `data/tools/` | System-level config |
| Config in `config.d/` | Anything outside volumes |

## Legacy support

If no `bootstrap.sh` exists, the daemon also checks for `data/setup.sh` (the previous name). Both work, but `bootstrap.sh` takes priority.

## What's next?

- [Container Packages](docs:container-packages) -- detailed package installation guide
- [Getting Started](docs:getting-started) -- ALF setup and overview
