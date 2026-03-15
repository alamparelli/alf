---
category: Basics
tags: terminal, shell, docker, bash, container, persistence
order: 5
---

# Terminal

The Terminal tab opens a shell session **inside the Docker container** where ALF runs. It is not a shell on your host machine.

## What you're connected to

You get a bash session inside the ALF container as the `alf` user. The container runs Debian with many tools pre-installed — see [Packages & Startup](docs:bootstrap) for the full list.

## What persists

Files inside `~/data/` survive container rebuilds. Everything else (apt packages, binaries, shell config) is lost on `alf upgrade`.

| Persistent (`~/data/`) | Lost on rebuild |
|------------------------|-----------------|
| `tools/`, `skills/`, `apps/` | apt packages (reinstalled via `packages.txt`) |
| `context/`, `config.d/` | Binaries in `/usr/local/bin` |
| `logs/` | Shell config (`.bashrc` edits) |
| pip/npm packages (cache volumes) | System-level configuration |

To make system packages permanent, add them to `config.d/packages.txt` — see [Packages & Startup](docs:bootstrap).

## Tips

- Use the terminal to test commands before making them permanent
- The terminal supports themes — use the dropdown in the toolbar
- Click **New Session** to open a fresh shell
- The terminal auto-resizes to fit your browser window

## What's next?

- [Packages & Startup](docs:bootstrap) — install packages and configure startup
- [Building Tools & Extensions](docs:container-packages) — create tools and install packages
