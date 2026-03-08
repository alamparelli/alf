---
category: Basics
tags: terminal, shell, docker, bash, container, bootstrap, persistence
order: 5
---

# Terminal

The Terminal tab opens a shell session **inside the Docker container** where ALF runs. It is not a shell on your host machine.

## What you're connected to

When you open a terminal, you get a bash session inside the ALF container (node:22-slim) as the `node` user. You can switch to root with `su -` if needed.

The container's filesystem is ephemeral — most of it is rebuilt on every `alf upgrade`.

## Persistence

Anything you install or configure directly in the terminal (apt packages, pip packages, binaries, shell config) **will be lost** when the container is rebuilt.

To make changes permanent, add them to `data/bootstrap.sh`. This script runs automatically at every daemon startup and re-applies your customizations.

### Example workflow

```bash
# 1. Test your install in the terminal
apt-get install -y jq
jq --version   # works!

# 2. Once confirmed, add it to bootstrap.sh so it survives rebuilds
echo 'apt-get update -qq && apt-get install -y --no-install-recommends jq' >> ~/data/bootstrap.sh
```

### What persists without bootstrap.sh

Files inside `~/data/` are mounted as a Docker volume and survive rebuilds:

| Persistent (`~/data/`) | Lost on rebuild |
|------------------------|-----------------|
| `tools/`, `skills/`, `pages/` | apt/pip/npm packages |
| `context/`, `config.d/` | Binaries in `/usr/local/bin` |
| `bootstrap.sh` | Shell config (`.bashrc` edits) |
| `logs/` | System-level configuration |

## Tips

- Use the terminal to test commands before adding them to `bootstrap.sh`
- The terminal supports themes — use the dropdown in the toolbar to switch color schemes
- Click **New Session** to open a fresh shell
- The terminal auto-resizes to fit your browser window

## What's next?

- [Bootstrap Script](docs:bootstrap) -- automate container setup on every startup
- [Building Tools & Extensions](docs:container-packages) -- create tools and install packages
