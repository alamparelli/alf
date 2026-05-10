---
category: Development
tags: isolation, sandbox, layers, trust, signatures, permissions, WASM, kinds, security, 0.8.0
order: 12
---

# Isolation Model

How ALF protects your system when running tools and apps. Read this before asking ALF to create any capability (tool, app, skill) — picking the wrong kind or the wrong permissions is the most common cause of "it doesn't work" or "it has too much access."

You don't need to know the implementation. You need to know two things: **which kind to pick**, and **what permissions to declare**.

## The 3 layers

Every tool, app, and skill ALF executes passes through 3 layers:

| Layer | What it does | What you control |
|---|---|---|
| **1. Walls** | Code runs inside ALF's locked-down container. WASM-kind bundles get an extra wall (a per-module sandbox) around their own binary. | Pick the right kind (see below) |
| **2. Identity** | Every loadable bundle is signed. ALF refuses to load anything unsigned. | Build locally → auto-signed. Import external → `alf trust add` the publisher key once. |
| **3. Authority** | A bundle only gets the permissions its manifest declares — file paths, network, vault keys. Everything else is unreachable. | Declare the minimum in the manifest. |

**There is no "dev mode unsigned" flag.** The model is the same in dev and in production. The daemon signs your local builds with its own bootstrap key — that key lives in your trust store like any other.

## The 3 kinds — which one should you build?

ALF has three ways to package executable logic. The right kind depends on **who wrote it** and **what it needs to do**.

| Kind | Who authors | Best for | Lives in |
|---|---|---|---|
| **bash / Python tool** | Maintainer only (ships with the daemon, runs in TCB) | Quick glue scripts, file helpers, wrappers over existing CLI binaries | `~/data/tools/<name>` + `<name>.json` |
| **Go-kind app** | Maintainer (compiled at install) | App shells with HTML/JS frontend + optional Go backend, marketplace apps | `~/data/apps/<slug>/` |
| **WASM-kind tool/app** | Anyone — third-party, LLM-authored, untrusted code | Isolated tools, third-party logic, anything that should not have ambient daemon access | `~/data/skills.d/wasm/<id>/` |

**Decision rule** — when in doubt, pick **WASM-kind**. The 0.8.0 architecture mandates it for any non-maintainer capability. Maintainer bash/Python tools are reserved for code shipped as part of the daemon distribution.

### Quick decision tree

1. **Does ALF write the tool for you, or is it imported from someone else?** → **WASM-kind**. Always.
2. **Does the maintainer (you, when editing the daemon distribution) ship it as a built-in utility?** → **bash / Python**.
3. **Does it need a frontend + backend with its own UI tab?** → **Go-kind app**.
4. **Anything else** → **WASM-kind**.

## Trust and signing

Most of the time, you don't manage signatures manually. Here's the flow:

| Source | What happens |
|---|---|
| **You build it locally** (via `tool-creator`, `sdk-app-builder`, or `wasm-builder` skills) | Daemon auto-signs at next boot with its bootstrap key. The bundle just appears in your toolbox. |
| **You sign it explicitly** (`alf keygen` once + `alf sign <bundle-dir>`) | Your user-endorsed signature lets you grant permissions the daemon's bootstrap key cannot. Needed for pre-publication testing. |
| **You install from a marketplace** | Bundles ship pre-signed. You add the publisher's key once with `alf trust add`. Every future bundle from that publisher loads automatically. |
| **A bundle has no signature** | ALF skips it at boot. No exception. |

**Marketplace is not a privileged channel.** It is just another key in your local trust store. A bundle you signed yourself and a bundle from the marketplace go through the exact same verification.

`alf trust add`, `alf trust list`, and `alf trust remove` are **admin operations** — they require a TTY or an authenticated Control Center session. ALF cannot run them on its own from a chat turn.

## The permission ceiling

The permissions a bundle can declare depend on **which key signed it**:

| Signed by | Allowed permissions (0.8.0) |
|---|---|
| Daemon-bootstrap key (auto) | `fs.reads`, `fs.writes` only |
| User-endorsed key (you) | Everything — you approved it explicitly |
| Marketplace key (third-party) | Everything (their publisher chose; you trust them by adding their key) |

If you want broader permissions on a WASM tool (the `http`, `exec`, and `secrets` blocks land in 0.9.0), sign it yourself:

```bash
alf keygen --name my-key                # one-time setup
alf sign ~/data/skills.d/wasm/<id>/     # sign each bundle
```

## What you can never do

- Run an unsigned bundle. No flag, no env var.
- Grant a bundle a permission its manifest doesn't declare — the host function isn't even linked in.
- Reach across capability boundaries. A WASM tool can't read another tool's data dir.
- Bypass the container walls, even with a maintainer bash tool.

## What you control

- Which **kind** you pick (or which one ALF picks for you when you ask it to create a tool).
- Which **permissions** the manifest declares — request the minimum.
- Which **publishers** you trust (`alf trust add` / `alf trust list` / `alf trust remove`).
- Whether you let the daemon sign with its bootstrap key (automatic, scoped) or sign with your own key (full ceiling).

## What ALF does for you

When you ask ALF to build something, the relevant skill (`tool-creator`, `sdk-app-builder`, or `wasm-builder`) takes care of:

1. **Choosing the kind** based on authorship and use case.
2. **Writing the manifest** with the minimum permissions needed.
3. **Building locally** — WASM bundles via `wasm_build_tool`, Go apps via `go build` at install, bash/Python with no build.
4. **Triggering the load** — restart the daemon or wasm-loader so the bundle is auto-signed and registered.
5. **Reporting back** — if you see `[wasm-loader] registered <id>` (or the equivalent for other kinds) in the logs, it worked.

If a step fails, the bundle is skipped and a log line tells you why. See the troubleshooting section in [Creating WASM Tools](docs:wasm-tools) for common errors.

## What's next

- [Building Tools & Extensions](docs:container-packages) — the kind decision tree applied in practice, plus bash/Python tools
- [Creating WASM Tools](docs:wasm-tools) — user guide for WASM-kind tools and apps
- [Building Marketplace Apps](docs:marketplace-apps) — Go-kind apps and marketplace publishing
- [App Permissions](docs:app-permissions) — what Go-kind apps see and don't see
