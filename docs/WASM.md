# ALF WASM Runtime

> Companion to `ARCHITECTURE-SECURITY.md`. That doc says *why* capabilities are isolated; this one says *how* WASM-kind capabilities are built, signed, loaded, invoked, and revoked. Scope: the 0.8.0 milestone.

---

## 1. Scope & audience

This document is the implementation reference for WASM capabilities in ALF. It is written for contributors working on any 0.8.0 sub-ticket that touches the forge, handles, host functions, guest build, or manifest.

It is **not**:
- A tutorial for end users writing their first capability (that comes later, as part of #389)
- A marketing overview of WASM (use the architecture doc's §2.1 for that)
- A replacement for the cryptographic trust model spec (#387 owns that)

If a design decision is in conflict between this doc and `ARCHITECTURE-SECURITY.md`, the architecture doc wins and this doc is a bug.

---

## 2. Why wazero

**Deny-by-default imports are the physical mechanism of Tier 3.1.** A wazero module receives zero host functions unless the embedder explicitly links them. `Runtime.Instantiate` reads the verified manifest and calls `NewHostModuleBuilder` with only the functions backed by forged handles. A guest binary cannot reach a host function that was not linked — this is structural, not a runtime check.

Secondary reasons:
- **Pure Go, no cgo.** Ships in the daemon binary, no separate dynamic loading.
- **Deterministic execution.** Epoch-based fuel, bounded memory, stable behavior across platforms.
- **Bounds-checked guest memory.** `Memory.Read` / `Memory.Write` enforce bounds at the wazero level; host functions never dereference raw guest pointers.

### Version pinning

Per `ARCHITECTURE-SECURITY.md` §2.1, Layer 1 inner ring's correctness is wazero's correctness. Policy:

- **Pinned version**: `v1.11.0` (declared in `go.mod`).
- **Upgrade cadence**: patch upgrades reviewed quarterly; minor/major require re-running the prototype test battery (`make test-wasm-prototype`) and reviewing the CVE list for the new version.
- **CVE tracking**: watch https://github.com/tetratelabs/wazero/security/advisories. Any High/Critical against a pinned version triggers an immediate upgrade evaluation.
- **Fuzzing**: wazero ships its own fuzzing; we do not fuzz the library ourselves. We fuzz the *host function layer* (see §3.4).

---

## 3. Host-function ABI (alf-fs-v0)

### 3.1 Namespace

All host functions live in the wazero host module named `alf`. Function names are prefixed `alf_*`. Any imported symbol from module `alf` that doesn't start with `alf_` is rejected at load time by the import cross-check (§7.1).

Imports from `wasi_snapshot_preview1` are allowed unconditionally — the Go runtime needs them for init (clock, random, args, fd_write for panic messages). No filesystem pre-opens are configured; WASI cannot touch the host filesystem ambiently.

### 3.2 Function signatures (0.8.0 subset)

Only `fs` is supported in 0.8.0. `http`, `exec`, `secrets` handles are scoped to 0.9.0+.

```
alf_fs_read(path_ptr i32, path_len i32, out_ptr i32, out_max i32) → i64
  packed result:
    high 32 bits = err_code (see §3.3)
    low  32 bits = out_len  (bytes written, valid only when err_code == 0;
                             when err_code == errBufferTooSmall, contains
                             the required buffer size)

alf_fs_write(path_ptr i32, path_len i32, data_ptr i32, data_len i32) → i32
  result = err_code
```

**Why packed i64 returns**: Go's `//go:wasmimport` does not cleanly support multi-return across all target toolchains. Packing `err | len` in a single i64 keeps the host ABI uniform across guests and removes a source of incompatibility. Callers unpack with `(r >> 32)` and `(r & 0xFFFFFFFF)`.

### 3.3 Error codes

| Code | Name | Meaning |
|---|---|---|
| 0 | errOK | Operation succeeded; check `out_len` for bytes written |
| 1 | errRevoked | Handle was revoked — Instance.Close() was called |
| 2 | errOutOfScope | Path is not covered by manifest's fs.reads / fs.writes |
| 3 | errIO | Underlying filesystem error (permission denied, disk full, not found, etc.) |
| 4 | errBufferTooSmall | For read only: output buffer smaller than required; `out_len` carries the required size |

Error codes are **structural**, not enumerated in the manifest. They flow from the handle layer, not from the host function implementation — a host function cannot invent a new error class.

### 3.4 Host-function safety matrix

For each host function, the safety responsibilities:

| Function | Input validation | Scope enforcement site | Revocation site | Memory access |
|---|---|---|---|---|
| `alf_fs_read` | `Memory.Read(path_ptr, path_len)` returns ok | `handle.FSHandle.Read` via `scopeAllows` | `handle.FSHandle.preflight` (atomic.Bool + lifecycleCtx) | `Memory.Read` (path) + `Memory.Write` (output) — bounds-checked |
| `alf_fs_write` | Same pattern for path + data | `handle.FSHandle.Write` via `scopeAllows` | Same | `Memory.Read` (path + data) — bounds-checked |

**Hard rule** (archtest target for 0.8.0): host functions dereference guest memory *only* via `Memory.Read` / `Memory.Write`. No raw pointer arithmetic from guest-supplied offsets. This closes the audit finding C1 from the 0.7.9 security review.

### 3.5 Only linked if authorized

`buildHostModule` in `internal/runtime/wasm/host_fs.go` exports `alf_fs_read` only if `Instance.FS.Scope().Reads` is non-empty, and `alf_fs_write` only if `Scope().Writes` is non-empty. A guest whose manifest declares no fs access receives an `alf` host module with no functions — calls to `alf_fs_*` from the guest would fail to instantiate (unresolved import). This is why §7.1 cross-check is a belt-and-braces defense, not the only line.

---

## 4. Guest build

### 4.1 Toolchain requirements

```
GOOS=wasip1
GOARCH=wasm
-buildmode=c-shared
CGO_ENABLED=0
```

Go version: **1.24+** (earlier versions lack `//go:wasmexport` and reliable `-buildmode=c-shared` for wasip1).

### 4.2 Why `-buildmode=c-shared`

`go build` without `-buildmode=c-shared` produces a **command-mode** wasip1 module: the binary exports only `_start`, which runs `main()` and calls `proc_exit`. After `proc_exit`, the module is closed and subsequent calls to `//go:wasmexport` functions fail with `module closed`.

`-buildmode=c-shared` produces a **reactor-mode** module: the binary exports `_initialize` instead of `_start`. `_initialize` runs Go runtime init (GC setup, package `init()` functions) and returns. Main is never executed; the module stays resident and the host can call exported functions.

This is the canonical Go wasip1 reactor pattern — it is **not** a workaround. The prototype initially attempted command-mode with `select{}`-blocking main, which deadlocks instantiate; c-shared is the correct fix.

**Host-side config** (in `internal/runtime/wasm/instantiate.go`):

```go
modCfg := wazero.NewModuleConfig().
    WithName(b.Manifest.ID).
    WithStartFunctions("_initialize")
```

### 4.3 Pragma conventions

```go
//go:wasmimport alf alf_fs_read
func alfFsRead(pathPtr, pathLen, outPtr, outMax uint32) uint64

//go:wasmexport alf_alloc
func alfAlloc(size uint32) uint32 { ... }
```

- `//go:wasmimport <module> <name>` — guest imports a host function
- `//go:wasmexport <name>` — guest exposes a function to the host

`//export <name>` is the cgo directive and does **not** work for WASM exports. Using it produces a module with the function defined but not exported — silent failure.

### 4.4 GC keepalive pattern

Go's WASM runtime runs GC inside the guest. A buffer allocated in `alfAlloc` and returned to the host as a `uint32` pointer becomes unreferenced after `alfAlloc` returns — GC is free to reclaim it before the host writes into it.

The required pattern:

```go
var allocated = map[uint32][]byte{}

//go:wasmexport alf_alloc
func alfAlloc(size uint32) uint32 {
    buf := make([]byte, size)
    ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
    allocated[ptr] = buf  // keep alive until freed or guest exits
    return ptr
}
```

For the prototype, buffers are never freed — guests are short-lived enough that this is fine. Future work: add `alf_free(ptr)` once apps grow large enough for this to matter.

### 4.5 In-daemon build (`wasm_build_tool`)

All WASM capability builds happen inside ALF via the native tool `wasm_build_tool` (`internal/tooling/native_wasm_build.go`). This is the only supported path per `ARCHITECTURE-SECURITY.md` §4.1. There is no `build.sh` external to the daemon.

Flow:
1. LLM (or CLI) calls `wasm_build_tool` with `{manifest_toml, sources}`. The manifest is authoritative — `id` and `kind` are read from it, not taken as independent inputs.
2. Tool runs `envelope.Validate` on `manifest_toml` — rejects deferred blocks (http/exec/secrets/events/tools/memory) per MANIFEST-SCHEMA §3.4 and enforces required fields. Kind must be `wasm-tool` or `wasm-app`.
3. Tool runs `internal/runtime/wasm/builder.Build` — materialises sources in an isolated tempdir, runs the Go toolchain with the env from §4.1, returns the `.wasm` bytes. Tempdir removed on every return path. Context cancellation kills the subprocess.
4. Tool installs `manifest.toml` + `<id>.wasm` under `<DataDir>/skills.d/wasm/<id>/` (0o600 files, 0o700 dir). The bundle ships **unsigned** at this layer.
5. Tool returns a JSON status report: `{id, kind, bundle_dir, wasm_sha256, wasm_bytes, unsigned: true, signing_note}`.

**Import cross-check** is NOT run at build time. Running it here would require `internal/tooling` to import `internal/runtime/wasm`, which forms a cycle (`runtime` imports `tooling`). The invariant lives at instantiate time — the same `CheckImports` that gates `Runtime.Instantiate` (§7.1 step 3). The single-source-of-truth architecture holds: the loader is the one authoritative line of defence, and a build-time pre-flight would be developer convenience, not a security boundary.

**Signing** happens at boot-time discovery (§7.1-bis): the `Loader.LoadDir` sees an unsigned bundle, signs the canonicalised manifest + bundle hash with the §7.3 Tier 2 daemon key, persists `manifest.sig` alongside the bundle, and proceeds to the normal signed-load path. Subsequent boots reuse the persisted signature. Third-party bundles (marketplace, step 12 / #384) ship pre-signed and bypass auto-signing.

---

## 5. Manifest specification (0.8.0 subset)

### 5.1 Canonical form

```toml
id = "hello-read"
kind = "wasm-tool"
name = "hello-read"
description = "Reads a file from the hello-read scoped data dir."

[[fs.reads]]
path = "data/"

[[fs.writes]]
path = "data/notes.json"
```

**Fields:**

- `id` (required): unique capability ID, also the directory name under `skills.d/wasm/<id>/`
- `kind` (required): `"wasm-tool"` or `"wasm-app"`
- `name` (optional): display name
- `description` (optional): human-readable description (used in LLM tool schemas)
- `[[fs.reads]]` (repeated): each entry has `path` — relative paths resolve against the bundle's `baseDir`; trailing `/` means directory (prefix match); no trailing `/` means exact file
- `[[fs.writes]]` (repeated): same semantics as `fs.reads`

### 5.2 What's not yet in the canonical form

Deferred to other tickets, **do not add to manifests yet**:
- Signature block (#387 / #397)
- `[[http.scopes]]`, `[[exec.commands]]`, `[[secrets.scopes]]` (0.9.0+ Tier 3.1 expansion)
- `[[events.exports]]`, `[[events.subscribes]]` (#399)
- `[[tools.declares]]` (#389)

### 5.3 Canonicalization

The prototype uses a **minimal hand-written TOML parser** in `internal/runtime/wasm/manifest.go` that accepts exactly the subset documented above. This is deliberate — #397 will replace it with a canonicalized envelope + pinned reference parser + version tag. Until then: don't rely on TOML features not in the examples (no multiline strings, no inline tables, no mixed-type arrays).

---

## 6. Migration strategy per Kind

| Kind | 0.8.0 position | Trust model | Sandbox | Migration path |
|---|---|---|---|---|
| `KindTool` (native Go) | Maintainer-only code in alf repo | Release pipeline signature on the daemon binary — the tool ships with it | Layer 1 outer ring (#86) + Tier 3.1 discipline in the source | Stays Go. Third-party tools that exist today as Go-kind are rewritten as `KindWASMTool` before 0.8.0 final. |
| `KindWASMTool` | **New.** Default for all new / third-party tools | Per-bundle signature under Layer 2 | wazero (Layer 1 inner) + forged handles (Tier 3.1) | Authored via `wasm_build_tool`; installed under `skills.d/wasm/<id>/` |
| `KindSkill` | Stays | Skill bundle signed under Layer 2 | Skills don't execute code — they orchestrate tool calls via the LLM | Skill contents unchanged; tools *referenced* by skills become `KindWASMTool` |
| `KindApp` (native Go) | Deprecated path for new apps | Per-app signature under Layer 2 | Layer 1 outer + Tier 3.1 discipline | Existing apps (xpost, contacthive) continue to work through 0.8.0; new apps authored as `KindWASMApp` |
| `KindWASMApp` | **New.** Default for all new apps | Per-bundle signature under Layer 2 | wazero + forged handles | Authored via `wasm_build_app` (sibling to `wasm_build_tool`, same flow) |

**Policy**: after 0.8.0 final, no new `KindApp` or `KindTool` from third-party sources. Internal daemon code may still add `KindTool` (maintainer discipline), but any capability that originates outside the release pipeline must be WASM-kind. Archtest forbids dynamic Go plugin loading (`plugin` stdlib package) so a policy violation fails to compile.

---

## 7. Lifecycle

### 7.1 Instantiate flow

```go
// internal/runtime/wasm/instantiate.go — the single production
// load path from on-disk bytes to a running guest.
func (r *Runtime) Instantiate(ctx, in envelope.VerifyInput, wasmBytes, baseDir) (*Module, error) {
    // 1. envelope.Verify via runtime.Instantiator.InstantiateVerified:
    //    signature + trust store + schema + canonicalisation + bundle
    //    hash cross-check. Produces *VerifiedInstantiation (Instance +
    //    typed Manifest).
    vi, err := r.inst.InstantiateVerified(ctx, in, baseDir)
    if err != nil { return nil, err }

    // 2. Engine.Compile — wazero parses the guest bytes.
    cm, err := r.engine.Compile(ctx, wasmBytes)
    if err != nil { vi.Instance.Close(); return nil, err }

    // 3. Handle hygiene invariant #3 — CheckImports enforces that
    //    guest imports ⊆ manifest declarations.
    if err := CheckImports(cm, vi.Manifest); err != nil {
        _ = cm.Close(ctx); vi.Instance.Close(); return nil, err
    }

    // 4. Link host functions — only those backed by a live handle.
    //    BuildHostModule exports alf_fs_read iff scope.Reads non-empty,
    //    alf_fs_write iff scope.Writes non-empty.
    hostMod, err := BuildHostModule(ctx, r.engine.Runtime(), vi.Instance.FS)
    if err != nil { ... cleanup ... return nil, err }

    // 5. Instantiate guest (reactor mode — _initialize, not _start).
    modCfg := wazero.NewModuleConfig().
        WithName(string(vi.Manifest.ID)).
        WithStartFunctions("_initialize")
    guest, err := r.engine.Runtime().InstantiateModule(ctx, cm, modCfg)
    if err != nil { ... cleanup ... return nil, err }

    return &Module{Instance: vi.Instance, Manifest: vi.Manifest, Guest: guest, ...}, nil
}
```

- **The cross-check (step 3) prevents a manifest from lying**: if the `.wasm` imports `alf_fs_write` but the manifest declares only `fs.reads`, instantiate fails before any guest code runs (`ErrLyingManifest`).
- **envelope.Verify is the SOLE call site of the trust pipeline** — archtest `TestOneVerifyCallSite` enforces that `runtime.Instantiator.InstantiateVerified` is the one consumer. Every load path converges here.
- **The Runtime manages WASI**: `wasi_snapshot_preview1.Instantiate` is called once per `wasm.Runtime` at `NewRuntime`. Guests can freely link WASI imports; no host-FS pre-opens are configured, so WASI cannot reach the host filesystem ambiently.

### 7.1-bis Boot-time loader

`internal/runtime/wasm/loader.go` walks `<DataDir>/skills.d/wasm/<id>/` and registers each bundle it can verify:

```
<root>/<id>/manifest.toml   (required)
<root>/<id>/<id>.wasm       (required)
<root>/<id>/manifest.sig    (optional — auto-signed if absent)
```

Auto-sign path: LLM-authored bundles from `wasm_build_tool` (§4.5) ship unsigned. The loader signs with the §7.3 Tier 2 daemon key on first discovery, persists the signature, and reuses it on subsequent boots. Marketplace bundles (#384) ship pre-signed and bypass auto-signing.

Error aggregation: a per-bundle failure is logged and returned in the `errs` slice but never aborts the scan — one bad bundle cannot block others. Successful loads are registered in `capability.Registry` via `wasm.Adapter` so the LLM tool-loop sees WASM capabilities alongside native Go capabilities with no shim.

### 7.2 Invocation

Through `CapabilityAdapter.Execute` (`internal/runtime/wasm/adapter.go`):

1. Marshal input to JSON
2. Call `alf_alloc(len)` → guest pointer
3. `Memory.Write(ptr, payload)`
4. Call `alf_invoke(method_id, ptr, len)` → packed i64
5. Unpack `out_ptr` + `out_len`
6. `Memory.Read(out_ptr, out_len)` → response bytes
7. Copy (wazero shares memory) + unmarshal → `capability.Output`

The adapter is serialized per-module via a mutex — wazero module instances are not safe for concurrent invocation.

### 7.3 Revocation

`Instance.Close()`:
1. Cancels `lifecycleCtx` (via `context.WithCancel`)
2. Sets `revoked` atomic on each handle

Every handle method has a pre-flight check:
```go
func (h *FSHandle) preflight(ctx context.Context) error {
    if h.revoked.Load() { return ErrRevoked }
    // + lifecycleCtx + caller ctx checks
}
```

And every I/O operation races caller ctx, lifecycleCtx, and the goroutine performing the syscall — whichever completes first wins. This means in-flight reads/writes return promptly (empirically <100ms) when revoked; they don't drain first.

The prototype's `TestFSHandle_Revocation` + `TestE2E_Revocation_StopsSubsequentCalls` cover both the handle-level and module-level cascades.

### 7.4 Hot-reload

Rebuilding a capability (e.g. re-running `wasm_build_tool` with updated source):
1. `Runtime.Evict(ctx, bundleID)` — drops the compiled module from the cache, closes it
2. Loader re-reads `manifest.toml` + `<id>.wasm` from disk
3. `Runtime.Instantiate` with the new bundle

The old `Instance` is orphaned; callers holding it see `ErrRevoked` on next call. No ambient state migrates across reloads — state lives in files scoped via the handle or in guest memory (lost on reload by design).

---

## 8. What 0.8.0 WASM does not include

- **Threading inside guests.** wazero does not implement WASI threads. Guests are single-threaded.
- **Tier 3.1 handles beyond `fs`.** `http.Handle`, `exec.Handle`, `secrets.Handle` are designed in the architecture doc but implemented incrementally — 0.8.0 ships `fs` only; 0.9.0+ adds the rest.
- **WASI preview 2 / Component Model.** The ecosystem is not mature enough for alf to adopt it in 0.8.0. Revisit in 0.9.0+ — the current host ABI is a Preview 1 reactor.
- **Performance parity with native.** Each guest is ~2 MB. Instantiate takes ~60 ms. Per-call host overhead is <1 ms. For interactive tools this is fine; for hot-path use (embeddings, dedup, etc.) native stays faster.
- **Auto-rebuild on source change.** Developer loop is: edit source → call `wasm_build_tool` → `Runtime.Evict` → next invoke rebuilds. No file-watcher. Hot-reload on manifest change is out of scope.

---

## 9. Timeline & validation gates

### 9.1 Prototype validated + production rebuild

Branch `release-prototype/080` proved the architectural spine: forge, handles, cross-check, host ABI, revocation, in-daemon build, E2E tool + app (15 tests).

The production implementation on `release/0.8.0` is a clean rebuild — no direct copy of the prototype — that composes the pieces shipped in #388 (envelope verify) and #391 (ocap forge). It ships as 12 atomic commits under #386:

1. `InstantiateVerified` returns `VerifiedInstantiation` (handle.Instance + envelope.Manifest)
2. wazero v1.11.0 + `wasm.Engine` skeleton
3. `CheckImports` (handle hygiene invariant #3)
4. `host_fs` ABI (`alf_fs_read/write`, packed i64, `api.Memory.Read/Write` only)
5. `Runtime.Instantiate` pipeline
6. `wasm.Adapter` (behind `capability.Capability`)
7. `runtime/wasm/builder.Build` (Go→WASM via toolchain)
8. `wasm_build_tool` native tool
9. boot-time `Loader` + daemon-key auto-sign
10. `skills.d/wasm/hello-read/` reference tool + E2E round-trip
11. 3 archtests pinning wazero-import scope + host_fs memory rules
12. docs refresh (this commit)

**Test inventory**: 65 tests in `internal/runtime/wasm/` + 7 E2E + 3 archtests.

### 9.2 Phases

**Phase 0 — prerequisite** (see #404)
- Ship 0.7.9 live, freeze `release/0.7.9`, merge #385 quick-wins
- No demolition of the broken sandbox until phase 0 done

**Phase 1 — foundation**
- #377 (comms → runtime), #382 (facet wire-in), #383 (bypass elim + raze `sandbox/exec/linux.go`)

**Phase 2 — parallel tracks**
- Trust: #387 → #388 → #397
- Forge: #391 (can start with stubbed `trust.Verify`)
- WASM wiring: #386 (prototype integration — now mostly boot-time loader + registry glue)
- Marketplace: #384 (unblocked, spike direction confirmed)

**Phase 3 — Layer 3 completion**
- #389 skills, #392 providers, #398 hygiene completion, #399 events, #400 memory, #395 admin boundary

**Phase 4 — independent**
- #86 AppArmor + seccomp (Layer 1 outer)
- #396 revocation E2E (extends handle lifecycle already in prototype)

**Final gate — tag 0.8.0** ✅ shipped
- Strict ocap posture is now the default — no boot flag required
- `ALF_EXPERIMENTAL` dev-window gate retired; helper warns once and proceeds
- SECURITY.md updated to reflect new posture

### 9.3 Validation gates specific to the WASM layer

| After | Criterion |
|---|---|
| `#391` | Forge-only path, no `*memory.Store` / `*events.Bus` / `*tooling.Registry` in capability packages |
| `#398` | Handles non-serializable (`json.Marshal` returns error); WASM import cross-check archtest; no `unsafe` / `reflect` / `go:linkname` in capability packages |
| `#386` integration | `hello-read` loaded at daemon boot from `skills.d/wasm/`; LLM tool-loop sees it; `wasm_build_tool` registered as native tool |
| `#384` | Unsigned bundle refused; bundle signed by marketplace key verified through the same `trust.Verify` as local-signed |
| Tag 0.8.0 | strict ocap is the default boot posture (no flag required — dev-window `ALF_EXPERIMENTAL` gate retired with the strict-flip); regression + `test-wasm-prototype` + archtest all green |

---

## 10. Document lifecycle

This doc is a snapshot of the implementation state on `release/0.8.0`. It will drift as the remaining 0.8.0 tickets land. Mandatory refresh points:

- **After #388 lands** ✅ — stubbed `trust.Verify` language removed; §7.1 documents the real envelope pipeline.
- **After #391 lands** ✅ — handle type list reconciled; for 0.8.0 only `fs` is in scope (other handles shipped at the API level but the WASM host ABI wires only `fs` for now).
- **After #398 lands** — update §3.4 safety matrix with the final archtest ruleset (step 11 of #386 already pins the `host_fs.go` memory-access rules; #398 extends to the full capability-package set).
- **After #386 wiring lands** ✅ — §4.5 rewritten against the real `wasm_build_tool` + loader flow; §7.1 carries the real `Runtime.Instantiate` pipeline.
- **After daemon boot wiring lands** ✅ — `setupWASMLoader` runs unconditionally as of v0.8.0 final; the dev-window `ALF_EXPERIMENTAL=1` gate was retired and is no longer relevant.
- **At v0.8.0 tag** — §9 rewritten from roadmap to "what actually shipped"; §8 ("what's not included") moves items realised into the rest of the doc, keeps only what's truly deferred to 0.9.0+.

Doc owner for these refreshes: the person closing the corresponding ticket. If you close one of the above without refreshing this doc, you're shipping a lie to future contributors.

## 11. References

- `docs/ARCHITECTURE-SECURITY.md` — target architecture
  - §2.1 Layer 1 inner ring (wazero as wall)
  - §3.1 Tier 3.1 structural ocap (handles)
  - §4.1 Go-kind vs WASM-kind asymmetry
  - §4.2 Handle hygiene invariants
- Prototype branch: `release-prototype/080`
- Tickets: #386 (spike + integration), #391 (forge), #398 (hygiene), #404 (0.8.0 preparation)
- wazero: https://github.com/tetratelabs/wazero
- wazero security advisories: https://github.com/tetratelabs/wazero/security/advisories
- Go wasip1 reference: https://go.dev/blog/wasi + https://pkg.go.dev/cmd/compile (wasmimport/wasmexport pragmas)
