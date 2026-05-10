---
name: wasm-builder
description: Author WASM-kind capability bundles (tools and apps) for the 0.8.0 isolation model — host ABI, manifest envelope, build via wasm_build_tool, daemon auto-sign at boot
version: "1"
triggers: create wasm tool, build wasm capability, author wasm bundle, wasm-kind, wasm tool, wasm app, build a wasm, write a wasm tool, third-party tool, sandboxed tool
---

You are a WASM capability author for ALF 0.8.0. You produce signed, isolation-correct bundles that boot under the 3-layer security model.

## Step 0 — Decide if WASM-kind is the right fit

| User asks for… | Use this kind |
|---|---|
| A third-party tool, marketplace tool, isolated tool, sandboxed tool | **`wasm-tool`** |
| A long-running third-party app with its own state | **`wasm-app`** |
| A glue script the maintainer ships with the daemon, in TCB | `bash-tool` (see `tool-creator`) |
| An app shell with frontend + backend, maintainer-authored | Go-kind app (see `sdk-app-builder`) |
| A prompt template that orchestrates other tools | `skill` (see `skill-creator`) |

**Architectural rule** — `WASM-kind is mandatory for any third-party or LLM-authored capability.` Go-kind is reserved for code that ships in the daemon binary. Bash/Python tools are reserved for maintainer-authored utilities that operate within the daemon's TCB. See [`docs/ARCHITECTURE-SECURITY.md`](../../docs/ARCHITECTURE-SECURITY.md) §6.

If the user asks for "a tool" without specifying, ask one clarifying question: *"Is this a tool you want shipped as part of the daemon (bash/Python), or a third-party-style tool you want isolated under WASM?"* Default to WASM-kind on ambiguity — it is the safer 0.8.0 path.

---

## 1. Bundle structure (on-disk)

```
skills.d/wasm/<id>/
├── manifest.toml              ← envelope header + permissions (you write)
├── manifest.sig               ← detached signature (daemon auto-signs at boot)
├── <id>.wasm                  ← compiled reactor-mode binary (you build, see §4)
├── src/                       ← Go module retained for audit (you write)
│   ├── go.mod
│   └── main.go
└── data/                      ← scoped read/write dir; populated per manifest
```

**`<id>` is canonical** — same string in the directory name, in the manifest's `id` field, and in the compiled `.wasm` filename. Mismatches cause the loader to skip the bundle.

---

## 2. Manifest envelope

Minimal `wasm-tool` manifest — see [`docs/MANIFEST-SCHEMA.md`](../../docs/MANIFEST-SCHEMA.md) §3 + §4.1 for the full schema:

```toml
alf_envelope_version = 1

id          = "hello-read"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Hello Read"
description = "Read a file from the bundle's data directory."

[[fs.reads]]
path = "data/"
```

**Permissions are scoped paths, not a free-form string.** From [`MANIFEST-SCHEMA.md`](../../docs/MANIFEST-SCHEMA.md) §3.4:

- Trailing `/` = directory prefix (any file under it).
- No trailing `/` = exact file match.
- All paths are relative to the bundle root. No `..` segments. No symlinks escape.
- Only declare the permissions you actually need — the daemon's host functions are linked **only if your manifest declares them** ([`WASM.md`](../../docs/WASM.md) §3.5). An undeclared `alf_fs_write` is unresolvable at link time and the bundle fails to instantiate.

**0.8.0 supports** these permission blocks for `wasm-tool`/`wasm-app`:

- `[[fs.reads]]` — read scope
- `[[fs.writes]]` — write scope

`http`, `exec`, `secrets` blocks are **deferred to 0.9.0** ([`MANIFEST-SCHEMA.md`](../../docs/MANIFEST-SCHEMA.md) §5.1) and rejected by the envelope validator if present in 0.8.0 manifests with a daemon-key (Tier 2) ceiling.

---

## 3. Go ABI contract

### 3.1 Reactor mode

Every WASM bundle compiles in **reactor mode** (`-buildmode=c-shared`), not command mode. See [`WASM.md`](../../docs/WASM.md) §4.2 for why.

```go
//go:build wasip1

package main

import "unsafe"

// main() is REQUIRED but NEVER CALLED. Reactor mode runs _initialize
// once at instantiation; the host then calls //go:wasmexport functions
// on each invocation. Putting setup logic in main() = it never runs.
// Use package init() or module-level vars instead.
func main() {}
```

### 3.2 Required exports

```go
//go:wasmexport alf_alloc
func alfAlloc(size uint32) uint32 {
    if size == 0 {
        return 0
    }
    buf := make([]byte, size)
    ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
    allocated[ptr] = buf  // GC keepalive — see §3.4
    return ptr
}

//go:wasmexport alf_invoke
func alfInvoke(inPtr, inLen uint32) uint64 {
    // 1. Read input JSON from guest memory at (inPtr, inLen)
    // 2. Do work — call host imports as needed
    // 3. Write response JSON into a stable buffer
    // 4. Return packed (out_ptr << 32) | out_len
}
```

### 3.3 Host imports — the `alf` module

Match each `//go:wasmimport alf <fn>` to a permission block in the manifest. From [`WASM.md`](../../docs/WASM.md) §3.2:

```go
//go:wasmimport alf alf_fs_read
func alfFsRead(pathPtr, pathLen, outPtr, outMax uint32) uint64
// Returns packed (errCode << 32) | bytesWritten.

//go:wasmimport alf alf_fs_write
func alfFsWrite(pathPtr, pathLen, dataPtr, dataLen uint32) uint32
// Returns errCode.
```

Error codes ([`WASM.md`](../../docs/WASM.md) §3.3): `0=ok`, `1=revoked`, `2=out_of_scope`, `3=io`, `4=buffer_too_small`.

### 3.4 GC keepalive pattern

Go's WASM runtime runs garbage collection inside the guest. A buffer with no live reference can be reclaimed before the host writes to it. Always retain the slice:

```go
var allocated = map[uint32][]byte{}  // pointer → backing slice
```

Required in every bundle. For 0.8.0 there is no `alf_free` — buffers stay alive for the module lifetime, which is fine because tool invocations are short-lived.

### 3.5 Manifest lies are caught at instantiate time

If your binary has `//go:wasmimport alf alf_fs_write` but the manifest declares only `[[fs.reads]]`, the daemon link step fails with `ErrLyingManifest` and the bundle never runs ([`WASM.md`](../../docs/WASM.md) §7.1). Keep imports and manifest in sync.

---

## 4. Build flow

### Option A — LLM-facing: `wasm_build_tool` (recommended)

The daemon registers `wasm_build_tool` as a native tool. Call it with the manifest TOML and the source file map:

```json
{
  "manifest_toml": "alf_envelope_version = 1\nid = \"my-tool\"\nkind = \"wasm-tool\"\n...",
  "sources": {
    "go.mod": "module alf/my-tool\n\ngo 1.24\n",
    "main.go": "//go:build wasip1\n\npackage main\n..."
  }
}
```

The tool ([`internal/tooling/native_wasm_build.go`](../../internal/tooling/native_wasm_build.go)):
1. Validates the manifest (rejects deferred permission blocks, checks scope).
2. Compiles in an isolated tempdir with the daemon's pinned Go toolchain.
3. Installs the bundle under `<DataDir>/skills.d/wasm/<id>/`.
4. Returns `unsigned=true` — the daemon will auto-sign at next boot.

### Option B — Operator: manual `go build`

For local development or CI:

```bash
cd skills.d/wasm/<id>/src
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 \
  go build -buildmode=c-shared -trimpath -o ../<id>.wasm .
```

Match the flags exactly — see [`WASM.md`](../../docs/WASM.md) §4.1. Wrong flags produce a binary that fails to instantiate (no `_initialize` export) or a binary the daemon refuses to link.

After the build, the bundle is unsigned. Restart the daemon (or trigger a wasm-loader rescan); the boot loader will auto-sign with the daemon-bootstrap key.

---

## 5. Trust model — three tiers

[`docs/ARCHITECTURE-SECURITY.md`](../../docs/ARCHITECTURE-SECURITY.md) §7.3:

| Tier | Key source | How a bundle gets there | Permission ceiling |
|---|---|---|---|
| **Tier 2** | Daemon-bootstrap key (auto-generated, in `<dataDir>/keys/daemon.json`) | Built locally → daemon auto-signs at boot if `manifest.sig` is absent | `[[fs.*]]` only in 0.8.0 |
| **Tier 3** | User-endorsed key (operator generates with `alf keygen`, persists in trust store) | Operator runs `alf sign <bundle-dir>` after reviewing the manifest | No ceiling — operator approved everything they signed |
| **Tier 4** | Marketplace key (third-party, imported via `alf trust add`) | Bundle ships pre-signed from marketplace | Same path as Tier 3, just a different signer |

**Marketplace is not a privileged channel.** It is just another key in the local trust store. A bundle the user signs themselves and a bundle the marketplace signs traverse the same `trust.Verify` code path ([`internal/capability/envelope/verify.go`](../../internal/capability/envelope/verify.go)).

**Signature is always mandatory.** There is no "dev mode unsigned" flag. The auto-sign at boot is not a bypass — it is the daemon endorsing locally-built bundles with its own key, which is itself in the trust store.

---

## 6. Example end-to-end — `hello-read`

The 0.8.0 reference bundle lives at [`skills.d/wasm/hello-read/`](hello-read/). Read its source for a working pattern.

**Layout:**
```
skills.d/wasm/hello-read/
├── manifest.toml
├── hello-read.wasm
├── manifest.sig          (auto-signed at first boot)
└── src/
    ├── go.mod
    └── main.go
```

**`manifest.toml`** declares one `[[fs.reads]]` for `data/`. **`src/main.go`** imports only `alf_fs_read` (matching the manifest), uses the keepalive pattern, and returns JSON-shaped output.

**Boot output (success):**
```
[wasm-loader] auto-signed hello-read with daemon key <id>
[wasm-loader] registered hello-read
```

**Invocation:** the LLM calls the `hello-read` tool with `{"path": "data/notes.txt"}`. The daemon allocates a guest buffer via `alf_alloc`, writes the input JSON, calls `alf_invoke`, reads back the response.

---

## 7. Pitfalls

- **`func main()` does work** — never called in reactor mode. Use `init()` or module-level vars.
- **Forgetting the keepalive map** — buffers GC'd before host writes. Always store allocated slices.
- **Building without `-buildmode=c-shared`** — produces command mode (`_start` instead of `_initialize`). Bundle fails to instantiate.
- **Absolute paths in the manifest** — rejected. Use `data/foo` not `/home/alf/data/apps/<id>/data/foo`.
- **Importing a host function the manifest doesn't declare** — link-time `ErrLyingManifest`, bundle fails to load.
- **Using `unsafe`/`reflect` outside the keepalive allocator** — flagged by archtest (#398).
- **Calling `os.Open` / `ioutil` / Go stdlib filesystem ops** — these go through WASI, which has no pre-opened FDs. Use `alf_fs_read` / `alf_fs_write`.
- **Putting work in `main()`** — empty `main()` is required, work goes in `alf_invoke`.

---

## 8. Verification checklist

Before declaring the bundle done:

- [ ] `<id>` is identical in directory name, manifest `id`, and `.wasm` filename.
- [ ] Manifest declares only `fs.reads` / `fs.writes` blocks (no `http`/`exec`/`secrets` in 0.8.0).
- [ ] Every `//go:wasmimport alf <fn>` has a matching permission block in the manifest.
- [ ] `func main() {}` is empty.
- [ ] `alf_alloc` and `alf_invoke` are exported with `//go:wasmexport`.
- [ ] The `allocated` keepalive map is in place.
- [ ] Build command uses exactly `GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -trimpath`.
- [ ] After deploy + restart, daemon logs show `[wasm-loader] registered <id>` (not just "auto-signed").

---

## References

- [`docs/ARCHITECTURE-SECURITY.md`](../../docs/ARCHITECTURE-SECURITY.md) — 3-layer / 3-tier model, trust model
- [`docs/WASM.md`](../../docs/WASM.md) — host ABI, build flags, lifecycle
- [`docs/MANIFEST-SCHEMA.md`](../../docs/MANIFEST-SCHEMA.md) — full envelope spec, per-kind shapes, permission ceiling
- [`skills.d/wasm/hello-read/`](hello-read/) — working reference bundle
- [`internal/runtime/wasm/builder/builder.go`](../../internal/runtime/wasm/builder/builder.go) — build pipeline (read-only reference)
- [`internal/tooling/native_wasm_build.go`](../../internal/tooling/native_wasm_build.go) — `wasm_build_tool` implementation
