---
category: Development
tags: WASM, isolated tools, third-party, marketplace, capability bundles, signed, wasm-tool, wasm-app
order: 13
---

# Creating WASM Tools

WASM-kind tools and apps are the isolated execution path for ALF. They are **required** for any third-party logic, LLM-authored bundles, or anything that should not have ambient daemon access. See [Isolation Model](docs:isolation-model) for the mental model.

## When to use WASM-kind

| User intent | Kind |
|---|---|
| "Add a tool that does X" — ALF writes it for you | `wasm-tool` |
| "Build me an isolated app with its own state" | `wasm-app` |
| Shell glue, maintainer utility, single-shot script | bash/Python — see [Tools & Extensions](docs:container-packages) |
| Frontend + backend app shell, marketplace publishing | Go-kind — see [Building Marketplace Apps](docs:marketplace-apps) |

**Default to WASM-kind on ambiguity.** It is the safer 0.8.0 path.

## How to ask ALF to create one

The `wasm-builder` skill handles the entire flow. Tell ALF what you want:

```
Create a wasm tool that reads my notes file and counts lines per tag
```

ALF will:

1. Choose `wasm-tool` (single invocation) vs `wasm-app` (long-running with state).
2. Write a manifest with the minimum `fs.reads` / `fs.writes` paths needed.
3. Generate Go source matching the WASM reactor-mode ABI.
4. Call `wasm_build_tool` to compile locally with the daemon's pinned toolchain.
5. Trigger a wasm-loader rescan; the daemon auto-signs at boot.

You'll see the bundle appear in `~/data/skills.d/wasm/<id>/` and the daemon log will print `[wasm-loader] registered <id>`.

## Bundle layout

```
~/data/skills.d/wasm/<id>/
├── manifest.toml         # envelope + permissions
├── manifest.sig          # signature (auto-signed at boot)
├── <id>.wasm             # compiled binary
└── src/                  # Go source (retained for audit)
    ├── go.mod
    └── main.go
```

The `<id>` is canonical — same string in directory name, manifest `id` field, and `.wasm` filename. Mismatch = bundle silently skipped at load.

## Permissions in 0.8.0

WASM bundles can declare these permission blocks today:

| Block | What it grants |
|---|---|
| `[[fs.reads]]` | Read access to the listed paths (relative to bundle root) |
| `[[fs.writes]]` | Write access to the listed paths (relative to bundle root) |

`http`, `exec`, and `secrets` blocks are deferred to 0.9.0. A 0.8.0 bundle declaring those is rejected at envelope validation.

**Path rules:**

- Trailing `/` = directory prefix (any file under it).
- No trailing `/` = exact file match.
- All paths are relative to the bundle root. No `..` segments. No symlinks that escape.

Example minimal manifest:

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

**One bundle, one purpose.** WASM bundles are cheap. Don't pack three unrelated tools in one bundle just to share state — share via files in `data/` if you really need to.

**Restart the wasm-loader after a rebuild.** That's when the bundle is auto-signed and registered. ALF does this for you when it builds via the skill; if you build manually, run `alf restart` or trigger the loader explicitly.

**Verify it loaded.** Look for `[wasm-loader] registered <id>` in the daemon log. If you only see `[wasm-loader] auto-signed <id>` without the `registered` line, instantiation failed — check the log for the precise error.

## Trust and signing

By default the daemon signs your locally-built bundles at boot with the **daemon-bootstrap key** (Tier 2). This is enough for any permission you can declare in 0.8.0 (`fs.reads` / `fs.writes`).

If you want broader permissions (in 0.9.0 once `http`/`exec`/`secrets` land) or want to publish to a marketplace, sign with a **user-endorsed key** (Tier 3):

```bash
alf keygen --name my-key                # one-time setup
alf sign ~/data/skills.d/wasm/<id>/     # sign explicitly
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
| `ErrLyingManifest` | A `//go:wasmimport alf <fn>` in the binary has no matching permission block in the manifest. Add it. |
| `ErrUntrustedKey` | Signed by a key not in your trust store. Add it with `alf trust add`, or rebuild locally to get the daemon signature. |
| `ErrPermissionCeiling` | Manifest declares a permission the signing key's tier doesn't allow. Sign with a higher-tier key. |
| `ErrInvalidEnvelope` | Manifest has unsupported fields (e.g. `http`/`exec`/`secrets` in 0.8.0). Remove them. |
| `id mismatch` | Directory name, manifest `id`, or `.wasm` filename don't match. Make all three identical. |

## Common pitfalls

- **Putting work in `main()`** — WASM bundles run in reactor mode. `main()` is required but never called. Setup goes in `init()` or module-level vars; per-invocation work goes in the `alf_invoke` export.
- **Forgetting the GC keepalive** — allocated guest buffers must be retained or Go's GC reclaims them before the host writes. The `wasm-builder` skill emits this pattern automatically; don't strip it.
- **Using `os.Open` or stdlib filesystem** — those go through WASI which has no pre-opened FDs in this model. Use the `alf_fs_read` / `alf_fs_write` host imports instead.
- **Absolute paths in the manifest** — rejected. Always use relative paths like `data/foo`, never `/home/alf/data/...`.

The `wasm-builder` skill enforces these patterns automatically. If you're hand-editing source, double-check before rebuilding.

## What's next

- [Isolation Model](docs:isolation-model) — the 3 layers and the trust model
- [Building Tools & Extensions](docs:container-packages) — bash/Python tools and the kind decision tree
- [Building Marketplace Apps](docs:marketplace-apps) — Go-kind apps and the marketplace
