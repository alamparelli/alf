---
category: Development
tags: isolation, sandbox, layers, trust, signatures, permissions, WASM, kinds, security, 0.8.0, doctrine
order: 12
---

# Isolation Model

How ALF protects your system when running tools and apps. Read this before asking ALF to create or install any capability — the rules below are enforced **structurally**, not by convention.

You don't need to know the implementation. You need to know two things: **where the code is allowed to come from**, and **what permissions it can declare**.

## The 3 layers

Every tool, app, and skill ALF executes passes through 3 layers:

| Layer | What it does |
|---|---|
| **1. Walls** | Code runs inside ALF's locked-down container. WASM bundles get an extra wall (a per-module sandbox) around their own binary. |
| **2. Identity** | Every loadable bundle is signed. ALF refuses to load anything unsigned. There is no dev-mode bypass. |
| **3. Authority** | A bundle only gets the permissions its manifest declares — file paths, network, vault keys. Everything else is unreachable. |

## Where code can come from — the kind rule

This is the rule most users get wrong. ALF separates **in-binary code** (maintainer-built, lives inside the daemon binary itself) from **disk-loadable code** (loaded at runtime from your data directory).

> **Doctrine (`ARCHITECTURE-SECURITY.md` §4.1):** Go-kind capabilities are reserved for code in the alf repository, built by the release pipeline, signed by the release key. **No dynamic Go plugins, no third-party Go-kind capabilities ever.** Anything loaded from disk at runtime is WASM-kind.

This means there is no "kind ceiling per signing key" — that would conflict with the §5 ceiling model. Instead, the rule is **structural**:

| Code lives in… | Allowed kinds | Loaded by |
|---|---|---|
| `alf-daemon` binary (in-binary, image-baked) | `bash-tool`, `python-tool`, `go-tool`, `go-app` | Registered at boot via the capability adapter — no disk loader |
| `/opt/alf/tools.d/` (image-baked symlinks) | `bash-tool`, `python-tool` (TCB, maintainer) | Tool discovery |
| `/opt/alf/skills.d/` (image-baked) | `skill` (prompt only) | Skills loader |
| `~/data/tools/<id>/` (your data) | `wasm-tool` only | WASM loader |
| `~/data/apps/<slug>/` (your data) | `wasm-app` only | WASM loader |
| `~/data/skills/<name>/` (your data) | `skill` (prompt only — no executable code) | Skills loader |

If you (or a marketplace) drop a Go binary in `~/data/apps/<slug>/`, there is **no on-disk loader that will pick it up**. The kind is a dead manifest. The WASM loader refuses any `kind` outside `{wasm-tool, wasm-app, skill, capability-provider, llm-provider}`.

**Consequence**: the only path for code you (or ALF, or a third party) can add at runtime is WASM-kind. Bash, Python, and Go tools/apps must ship inside the daemon binary or as image-baked symlinks under `/opt/alf/`.

## Trust and signing

Signatures attest **who** built the bundle. ALF maintains a local trust store; the daemon's bootstrap key is one entry among others.

| Source | What happens |
|---|---|
| **You ask ALF to build it** (`wasm-builder` skill) | Bundle is compiled locally, daemon auto-signs at boot with its bootstrap key (Tier 2). Loads. |
| **You sign it explicitly** (`alf keygen` once + `alf sign <bundle-dir>`) | Your user-endorsed signature (Tier 3) lets the bundle declare permissions the Tier 2 ceiling forbids. |
| **You install from a marketplace** | Bundle ships pre-signed (Tier 4). You add the publisher's key once with `alf trust add`. Future bundles from that publisher load automatically. |
| **A bundle has no signature** | ALF skips it at boot. No exception. |

**Marketplace is not a privileged channel.** It is just another key in your local trust store. A bundle you signed yourself and a bundle from the marketplace go through the exact same verification.

`alf trust add`, `alf keygen`, and `alf sign` are **admin operations** — they require a TTY or an authenticated Control Center session. ALF cannot run them on its own from a chat turn.

## The permission ceiling

Per-tier ceiling on **what permission blocks a signature can endorse**. This is orthogonal to the kind rule above (`MANIFEST-SCHEMA.md §5`).

| Signed by | Allowed permission blocks (0.8.0) |
|---|---|
| **Tier 2** — daemon-bootstrap key (auto) | `[[fs.reads]]` (own bundle + data dir), `[[fs.writes]]` (own data dir), `[[events.exports]]` (own topics) |
| **Tier 3** — user-endorsed key (you) | All blocks — you saw and approved them at sign time |
| **Tier 4** — marketplace key (third-party) | All blocks — each install asks for ratification |

If you want a WASM tool with broader permissions than Tier 2 allows (once `http`/`exec`/`secrets` blocks land in 0.9.0), sign with your own key:

```bash
alf keygen --name my-key                # one-time setup
alf sign ~/data/tools/<id>/             # sign explicitly
```

## What you can never do

- Run an unsigned bundle. No flag, no env var.
- Load Go-kind code from disk. The kind is unreachable from any on-disk loader. Only the daemon binary itself ships Go-kind.
- Drop a bash/Python script in `~/data/tools/` and expect it to run. That path is WASM-only.
- Grant a bundle a permission its manifest doesn't declare — the host function isn't even linked in.
- Reach across capability boundaries. A WASM bundle can't read another bundle's data dir.

## What you control

- Whether **ALF builds the bundle locally** (WASM auto-signed Tier 2) or you **sign it yourself** (Tier 3, full ceiling).
- Which **publishers** you trust (`alf trust add` / `alf trust list` / `alf trust remove`).
- Which **permissions** the manifest declares — request the minimum.

## What ALF does for you

When you ask ALF to build something, the `wasm-builder` skill takes care of:

1. **Choosing tool vs app** (`wasm-tool` for single invocation, `wasm-app` for long-running with state).
2. **Writing the manifest** with the minimum permissions needed.
3. **Building locally** via `wasm_build_tool` — pinned toolchain, deterministic output.
4. **Installing at the right path** — `~/data/tools/<id>/` or `~/data/apps/<slug>/`.
5. **Triggering the load** — the daemon auto-signs and registers.

If a step fails, the bundle is skipped and a log line tells you why. Common errors and fixes are in [Creating WASM Tools](docs:wasm-tools).

## Why this is locked down

The kind rule (`§4.1`) is **not a convention** — it is the only honest claim ALF can make about "no ambient authority for third-party code". Go-kind code can use `unsafe.Pointer`, `reflect`, `go:linkname` — it can reach into Runtime memory and bypass any ocap guard. WASM-kind cannot, structurally. Letting third-party Go-kind exist would be a hole at the bottom of the security model.

The legacy `marketplace-app` kind (for Go-kind apps from the marketplace) is **retired** per `MANIFEST-SCHEMA.md §3.3`. Any app that previously installed as `marketplace-app` is being migrated to `wasm-app`. If you have third-party Go-kind apps in `~/data/apps/<slug>/` from before this transition, they will refuse to load on the next upgrade — your data is preserved, but the binary won't run.

## What's next

- [Building Tools & Extensions](docs:container-packages) — when to use bash/Python (maintainer) vs WASM (everyone else)
- [Creating WASM Tools](docs:wasm-tools) — user guide for `wasm-tool` and `wasm-app`
- [Building Marketplace Apps](docs:marketplace-apps) — frontend + WASM backend pattern for marketplace apps
- [App Permissions](docs:app-permissions) — what WASM apps see and don't see
