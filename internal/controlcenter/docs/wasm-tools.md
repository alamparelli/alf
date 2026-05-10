---
category: Development
tags: WASM, isolated tools, third-party, marketplace, capability bundles, signed, wasm-tool, wasm-app
order: 13
---

# Creating WASM Tools

WASM-kind is **the only kind ALF will load from disk at runtime**. Anything you want to add to your instance — a tool ALF writes for you, an app from the marketplace, a script you compile yourself — must be a WASM bundle. See [Isolation Model](docs:isolation-model) for why (§4.1 doctrine).

## When to use which kind

| User intent | Kind | Lives in |
|---|---|---|
| "Add a tool that does X" — ALF writes it, called once per invocation | `wasm-tool` | `~/data/tools/<id>/` |
| "Build me an app with frontend + persistent state" | `wasm-app` | `~/data/apps/<slug>/` |
| Prompt-only instructions (no code) | `skill` | `~/data/skills/<name>/` |

There is no `bash-tool`, `python-tool`, or `go-tool` path at user-level. Those kinds are reserved for code that ships inside the daemon binary (the maintainer-built path). Adding bash/Python scripts to `~/data/tools/` will not be picked up — the WASM loader refuses any non-WASM kind.

## How to ask ALF to create one

The `wasm-builder` skill handles the entire flow. Tell ALF what you want:

```
Create a wasm tool that reads my notes file and counts lines per tag
```

ALF will:

1. Choose `wasm-tool` (single invocation) vs `wasm-app` (long-running).
2. Write a manifest with the minimum `fs.reads` / `fs.writes` paths needed.
3. Generate Go source matching the WASM reactor-mode ABI.
4. Call `wasm_build_tool` to compile locally with the daemon's pinned toolchain.
5. Install the bundle at the right path (`~/data/tools/<id>/` or `~/data/apps/<slug>/`).
6. Trigger a wasm-loader rescan; the daemon auto-signs at next boot.

You'll see the bundle appear in your data directory and the daemon log will print `[wasm-loader] registered <id>`.

## Bundle layout

```
~/data/tools/<id>/             ← for wasm-tool
├── manifest.toml              ← envelope + permissions
├── manifest.sig               ← signature (auto-signed at boot)
├── <id>.wasm                  ← compiled binary
└── src/                       ← Go source (retained for audit)
    ├── go.mod
    └── main.go
```

`wasm-app` bundles have the same layout but live in `~/data/apps/<slug>/`, and may add a frontend (`index.html`, `app.json`, assets) and a `service.json` if the app has a background task.

The `<id>` (for tools) or `<slug>` (for apps) is canonical — it must match the manifest `id` field and the `.wasm` filename. Mismatch = bundle silently skipped at load.

## Permissions in 0.8.0

WASM bundles can declare these permission blocks today:

| Block | What it grants |
|---|---|
| `[[fs.reads]]` | Read access to the listed paths (relative to bundle root) |
| `[[fs.writes]]` | Write access to the listed paths (relative to bundle root) |
| `[[events.exports]]` | Topics this capability emits |
| `[[tools.declares]]` | Other capability ids this capability is authorised to invoke (skill kind) |

`http`, `exec`, and `secrets` blocks are deferred to 0.9.0. A 0.8.0 bundle declaring those is rejected at envelope validation (parse-time error).

**Path rules:**

- Trailing `/` = directory prefix (any file under it).
- No trailing `/` = exact file match.
- All paths are relative to the bundle root. No `..` segments. No symlinks that escape.

Example minimal manifest for a `wasm-tool`:

```toml
alf_envelope_version = 1
id          = "notes-counter"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Notes Counter"
description = "Count lines per tag in user notes."

[[fs.reads]]
path = "data/notes/"

[[fs.writes]]
path = "data/cache/"
```

## Best practices

**Declare only what you use.** Host functions are linked at instantiation only if your manifest declares them. An undeclared import = `ErrLyingManifest` at load time, bundle never runs. Keep imports and manifest in sync.

**Use `data/` for your I/O surface.** Your bundle's `data/` directory is the only read/write surface you can scope. Use subdirectories to separate inputs and outputs (`data/inbox/`, `data/cache/`).

**One bundle, one purpose.** Bundles are cheap. Don't pack three unrelated tools in one bundle just to share state — share via files in `data/` if you really need to.

**Restart the wasm-loader after a rebuild.** That's when the bundle is auto-signed and registered. ALF does this for you when it builds via the skill; if you build manually, run `alf restart` or trigger the loader explicitly.

**Verify it loaded.** Look for `[wasm-loader] registered <id>` in the daemon log. If you only see `[wasm-loader] auto-signed <id>` without the `registered` line, instantiation failed — check the log for the precise error.

## Trust and signing

By default the daemon signs your locally-built bundles at boot with the **daemon-bootstrap key** (Tier 2). This is enough for any permission you can declare in 0.8.0 (`fs.reads`, `fs.writes`, `events.exports`).

If you want broader permissions (in 0.9.0 once `http`/`exec`/`secrets` land) or want to publish to a marketplace, sign with a **user-endorsed key** (Tier 3):

```bash
alf keygen --name my-key                # one-time setup
alf sign ~/data/tools/<id>/             # sign explicitly
```

Bundles you didn't author come signed by a **marketplace key** (Tier 4). Add the publisher's key once:

```bash
alf trust add <publisher-key.pub>
```

After that, every bundle from that publisher loads automatically.

`alf trust add`, `alf keygen`, and `alf sign` are admin operations — they require a TTY or an authenticated Control Center session. ALF cannot run them on its own from a chat turn.

## When something fails to load

Look at the daemon log on boot. Common errors:

| Log line | What's wrong |
|---|---|
| `ErrKindForbiddenByLoader` | Manifest declares a kind the on-disk loader does not accept. Only `wasm-tool`, `wasm-app`, `skill`, `capability-provider`, `llm-provider` are loadable from disk. |
| `ErrLyingManifest` | A `//go:wasmimport alf <fn>` in the binary has no matching permission block. Add the block or remove the import. |
| `ErrUntrustedKey` | Signed by a key not in your trust store. Add it with `alf trust add`, or rebuild locally to get the daemon signature. |
| `ErrPermissionCeiling` | Manifest declares a permission the signing key's tier doesn't allow (Tier 2 only signs `fs.*` and `events.exports` in 0.8.0). Sign with a higher-tier key. |
| `ErrInvalidEnvelope` | Manifest has unsupported fields (e.g. `http`/`exec`/`secrets` in 0.8.0). Remove them. |
| `id mismatch` | Directory name, manifest `id`, or `.wasm` filename don't match. Make all three identical. |

## Common pitfalls

- **Putting work in `main()`** — WASM bundles run in reactor mode. `main()` is required but never called. Setup goes in `init()` or module-level vars; per-invocation work goes in the `alf_invoke` export.
- **Forgetting the GC keepalive** — allocated guest buffers must be retained or Go's GC reclaims them before the host writes. The `wasm-builder` skill emits this pattern automatically; don't strip it.
- **Using `os.Open` or stdlib filesystem** — those go through WASI which has no pre-opened FDs in this model. Use the `alf_fs_read` / `alf_fs_write` host imports instead.
- **Absolute paths in the manifest** — rejected. Always use relative paths like `data/foo`, never `/home/alf/data/...`.
- **Declaring `kind: go-app` or `kind: marketplace-app`** — these kinds have no on-disk loader. The bundle parses but never runs.

The `wasm-builder` skill enforces these patterns automatically. If you're hand-editing source, double-check before rebuilding.

## What's next

- [Isolation Model](docs:isolation-model) — the 3 layers and the trust model
- [Building Tools & Extensions](docs:container-packages) — the maintainer-vs-user-code boundary
- [Building Marketplace Apps](docs:marketplace-apps) — frontend + WASM-app backend for marketplace publishing
