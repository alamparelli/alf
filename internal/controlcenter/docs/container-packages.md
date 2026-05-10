---
category: Development
tags: tools, apps, packages, setup, docker, build, WASM, kinds
order: 10
---

# Building Tools & Extensions

How to add executable capabilities to your ALF instance. Read this first — the rules below are enforced **structurally** by the daemon, not by convention.

## Where code can come from

ALF distinguishes **maintainer code** (built into the daemon binary, ships with the release) from **user code** (loaded at runtime from your data directory). These two paths have different rules:

| Source | Kinds allowed | How it gets there |
|---|---|---|
| **Maintainer (in-binary or image-baked)** — `alf-daemon`, `/opt/alf/tools.d/`, `/opt/alf/skills.d/` | `bash-tool`, `python-tool`, `go-tool`, `go-app`, `skill` (TCB) | Compiled or copied into the Docker image at release build time |
| **User (your data directory)** — `~/data/tools/<id>/`, `~/data/apps/<slug>/`, `~/data/skills/<name>/` | `wasm-tool`, `wasm-app`, `skill` (prompt-only) | Built by ALF via the `wasm-builder` skill, or installed from a marketplace |

The daemon's WASM loader scans `~/data/tools/` and `~/data/apps/` and **refuses any kind that is not `wasm-tool`, `wasm-app`, `skill`, `capability-provider`, or `llm-provider`**. There is no on-disk loader for Go-kind, bash-tool, or python-tool — those kinds only exist for code that ships inside the daemon binary itself.

See [Isolation Model](docs:isolation-model) for the doctrine behind this rule (§4.1).

## Quick decision tree

1. **ALF is writing this for you, or you're importing from a marketplace?** → **WASM-kind**. Ask ALF to use the `wasm-builder` skill. See [Creating WASM Tools](docs:wasm-tools).
2. **You want a full app with a UI tab + persistent backend?** → **WASM-app**. Same skill, `wasm-app` flavour.
3. **You want a prompt-only instruction set ALF follows when a topic comes up?** → **skill**. See [Creating Skills](docs:creating-skills).
4. **You're editing the daemon itself (maintainer mode, building a new release)?** → bash/Python/Go tools live in `/opt/alf/tools.d/` and `/opt/alf/skills.d/` inside the image. Outside the scope of this page.

There is no "drop a bash script in `~/data/tools/`" path anymore. That was the pre-lockdown model; bash/Python scripts at runtime would need to be signed, scoped, and run in a sandbox — at which point you've reinvented WASM-kind, badly. The daemon now refuses to register them.

## Directory layout

```
~/data/
├── tools/<id>/        ← WASM tools (signed bundles)
├── apps/<slug>/       ← WASM apps (signed bundles, may have frontend + service)
├── skills/<name>/     ← prompt-only skills (no executable code)
├── context/           ← files injected into every conversation
├── config.d/          ← read-only mount: tiers, agents, etc.
└── logs/              ← daemon logs

/opt/alf/              ← maintainer code (read-only mount inside container)
├── tools.d/           ← bash/Python tools shipped with the daemon (TCB)
├── skills.d/          ← prompt skills shipped with the daemon
└── ...
```

## Creating a tool or app

Ask ALF. The `wasm-builder` skill does the right thing — chooses tool vs app, writes the manifest, builds locally, installs in the right path, triggers the auto-sign on next boot.

```
Create a wasm tool that summarises my unread emails by sender
```

ALF will pick `wasm-tool` (single invocation) or `wasm-app` (long-running with state) based on what you describe, then drive the build. The output goes to `~/data/tools/<id>/` or `~/data/apps/<slug>/`. See [Creating WASM Tools](docs:wasm-tools) for the full spec.

You can also build by hand if you're comfortable with Go and the WASM reactor-mode ABI — same doc covers it.

## Creating a skill

Skills are instructions that ALF follows for specific topics — no executable code. See [Creating Skills](docs:creating-skills) for a full guide.

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

When the container image is rebuilt (`alf upgrade`), everything outside the volumes is lost. This includes pip packages, apt packages, and any binaries installed at runtime. This applies to **container packages**, not to ALF capabilities — WASM bundles in `~/data/tools/` and `~/data/apps/` survive rebuilds because they live in the data volume.

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

No bootstrap script needed — cache volumes (`~/.local`, `~/.npm`, `~/.cache`) survive restarts. Packages are only lost on image rebuild (`alf upgrade`), in which case ALF reinstalls them when needed.

## What survives a rebuild

| Survives | Lost on rebuild |
|----------|------|
| Everything in `~/data/` (tools, apps, skills, context, logs) | apt packages (reinstalled via `packages.txt`) |
| `config.d/packages.txt` | Binaries installed in `/usr/local/bin` at runtime |
| pip/npm packages (in cache volumes) | System-level config changes outside `~/data/` |

**Rule of thumb:** capabilities live in `~/data/`. System packages live in `config.d/packages.txt`. pip/npm live in cache volumes.

## Migrating from the legacy `~/data/tools/<name>` bash path

If you had bash or Python scripts in `~/data/tools/<name>` from before the lockdown:

1. The daemon now refuses to register them (kind unsupported from disk).
2. Your files are **not deleted** — they sit on disk, just not loaded.
3. Ask ALF to rewrite them as a `wasm-tool`: *"Migrate my `disk-check` script to a wasm tool."* The `wasm-builder` skill will read your script, port the logic to Go + WASM ABI, declare the right `fs.reads` / `fs.writes` paths, and install at `~/data/tools/disk-check/`.

Same path for `~/data/apps/<slug>/` Go-kind apps from the legacy marketplace — they refuse to load; ask ALF to migrate to `wasm-app`.

## What's next?

- [Isolation Model](docs:isolation-model) — why the lockdown is structural, not optional
- [Creating WASM Tools](docs:wasm-tools) — full guide for WASM bundles
- [Creating Skills](docs:creating-skills) — prompt-only skills
- [Tools Reference](docs:tools-reference) — built-in CLI tools available to your bundles
