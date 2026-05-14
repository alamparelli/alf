# Manifest schema (ALF 0.8.0)

> Companion to `ARCHITECTURE-SECURITY.md` §7 (Trust & vault) and §7.10 (Envelope & canonicalization). This document pins the **schema of the `manifest.toml` file** every capability bundle carries. `ARCHITECTURE-SECURITY.md` §7.10 pins the **canonicalization procedure** that turns this schema into the bytes signed by the bundle's detached signature.
>
> Delivered under ticket #397. Read alongside:
> - `#387` — the trust model that verifies signatures produced over canonicalized manifests
> - `#388` — the runtime implementation of load-time verification
> - `#391` — the ocap forge that consumes a verified manifest

---

## 1. Scope & audience

This doc is the implementation reference for:

- Authors of capability bundles (`wasm-tool`, `wasm-app`, `skill`, `provider`, `marketplace-app`)
- Implementers of the loader / verify path
- Reviewers of migration tooling (`alf migrate manifests` that converts legacy `manifest.json` to `manifest.toml`)

It is **not**:

- A user tutorial for authoring capabilities (those live in their own skill / marketplace docs)
- A description of capability runtime behaviour (see `docs/ARCHITECTURE-SECURITY.md` §3)
- A description of the signature envelope (see `ARCHITECTURE-SECURITY.md` §7.10)

## 2. Format & location

- **File format:** TOML v1.0 ([toml.io](https://toml.io)). No YAML, no JSON.
- **File name:** `manifest.toml` at the root of the bundle directory.
- **Parser:** [`pelletier/go-toml/v2`](https://github.com/pelletier/go-toml) at the version pinned in the daemon's `go.mod`. No alternative TOML parser may be imported by the verify path — archtest-enforced (`#397` delivers the archtest rule).

### 2.1 Why TOML (0.8.0 decision)

The 0.7.x codebase used `manifest.json` for marketplace apps. 0.8.0 unifies on TOML for every capability kind. Decision recorded on 2026-04-24:

- The prototype (`release-prototype/080`) already uses TOML for WASM bundles — keeping dual formats would fork the verify path.
- TOML's comment syntax is useful for manifest authoring in a way JSON-without-comments is not.
- 0.8.0 is a breaking ocap migration; adding "format change" to the breakage surface is a free move.
- A one-shot migration script (`alf migrate manifests`) converts existing `manifest.json` files; the conversion is mechanical.

### 2.2 Lenient parsing is rejected

Any TOML feature that allows the same logical data to have multiple byte representations is **parsed strictly or rejected**:

- Multi-line strings (`"""..."""`) — allowed, but the canonicalizer folds them to single-line with explicit escapes.
- Mixed-type arrays — rejected by the canonicalizer (fails the verify flow at step 9 of `ARCHITECTURE-SECURITY.md` §7.4).
- Inline tables — allowed (parser-normalised to nested tables).
- Trailing commas — allowed by TOML 1.0; the canonicalizer strips them.
- Comments — stripped at parse time (not part of the canonical form).
- Non-UTF-8 bytes — rejected at parse time (TOML 1.0 requires UTF-8).

The rule: **two manifests with the same logical content produce the same canonical bytes**, regardless of whitespace, key order, comment presence, or representation choice.

## 3. Schema

Every manifest has an **envelope header**, a **core section**, and zero or more **permission blocks** scoped by handle kind.

### 3.1 Envelope header (required in every manifest)

```toml
alf_envelope_version = 1
```

| Field | Type | Required | Description |
|---|---|---|---|
| `alf_envelope_version` | integer | yes | Schema version. `1` for 0.8.0. A daemon rejects unknown versions (future) and attempts forward-migration of older versions (past). |

Version changes:

- **Minor additions** (new optional fields): same version, daemon warns but accepts.
- **Breaking changes** (new required fields, semantics changes): increment the version.

### 3.2 Core section (required in every manifest)

```toml
id          = "hello-read"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Hello Read"
description = "Reads a file from the capability's scoped data dir."
```

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Unique capability ID within the registry. Matches `^[a-z0-9][a-z0-9-]*$`. Also the directory name under `<DataDir>/tools/<id>/` (kind=wasm-tool) or `<DataDir>/apps/<id>/` (kind=wasm-app) per §4.1 (#420). |
| `kind` | string | yes | One of the values in §3.3. |
| `version` | string | yes | Semver 2.0.0 of this bundle. Verification does not enforce semver ordering; the field is for audit and migration tooling. |
| `name` | string | yes | Human-readable display name. Surfaced in the Control Center UI and LLM tool schemas. |
| `description` | string | no | Freeform single-paragraph description. Used in LLM tool schemas so the routing classifier can decide when to invoke the capability. |

### 3.3 `kind` enumeration

```
wasm-tool             — short-lived WASM capability invoked from the LLM tool loop
wasm-app              — long-running WASM capability with a UI iframe
skill                 — prompt + orchestrated tool calls, no executable code (re-signed)
llm-provider          — LLM backend (Claude, OpenAI, Ollama, …) bundled as a capability
capability-provider   — exports new handle kinds into the runtime registry (#392)
marketplace-app       — legacy-Go app bundle; retired when Go-kind is replaced by WASM-kind
```

The legacy bare `provider` value (pre-0.8.0-beta) was split by `#392` into `llm-provider` and `capability-provider` because the two roles drive different sign-time ceilings, different runtime wiring, and different install-UX prompts. Conflating them under one kind would force runtime-content inspection of the `[provider]` block to disambiguate — the exact "schema-tells-you-what-it-means-via-content" anti-pattern this section forbids. Manifests carrying `kind = "provider"` are rejected with `ErrKindUnknown`.

Fields that become required / forbidden based on `kind` are listed in §4.

### 3.4 Handle-scoped permissions

Each handle kind (fs / http / exec / secrets / memory / events / tools) has its own block. Blocks are **optional** — a capability that declares no `[[fs.reads]]` cannot read any file.

**0.8.x wires `fs`, `events`, `tools`, and `http`** — schema, host import, forge, and runtime are all live (#421 Wave 1+2). Declaring `exec` or `secrets` blocks is a parse-time error until the corresponding handle kinds land in 0.9.0+. `[memory]` is deferred to `#400` Stage 2.

**Capability-extension blocks (`#392`)**: 0.8.0 also accepts three new top-level blocks for the user-extensible handle registry: `[provider]` (capability-provider only), `[[depends]]` (any kind, references provider-exported handle kinds), and `[[raw_imports]]` (any kind, escape-hatch for direct WASI imports). Stage 1 of `#392` validates them at parse time; runtime forge integration lands in subsequent stages.

#### fs — filesystem (0.8.0 scope)

```toml
[[fs.reads]]
path = "data/"

[[fs.reads]]
path = "config.json"

[[fs.writes]]
path = "data/notes.json"
```

| Field | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | Path relative to the capability's bundle root. Trailing `/` = directory prefix match. No trailing `/` = exact file match. Absolute paths rejected. |

Path semantics:

- Relative to the **bundle's scoped data directory** resolved by the forge at `Runtime.Instantiate` time.
- No `..` segments allowed — rejected at parse time.
- No symlinks allowed to escape the scope — enforced at open time by `internal/sandbox/exec/path.go`'s `CheckBoundary`.

#### tools — declared inter-capability invocation (#389)

```toml
[[tools.declares]]
id = "notify"

[[tools.declares]]
id = "log"
```

Each entry names another capability's `id` that this capability is
authorised to invoke through its forged `handle.ToolHandle`. Exact match
only — no wildcards, no globs. Listing every dependency by name is the
explicit-coupling rule the install-time UI relies on (and §3.1's "tools
outside `declares` are absent from the LLM tool surface" promise depends
on the same allowlist).

`id` rules:

- Matches `^[a-z0-9][a-z0-9-]*$` — same shape as a top-level
  `manifest.id`, since each entry references another capability.
- Duplicate ids in a single block are a parse error.
- An empty `[[tools.declares]]` array is valid and equivalent to
  omitting `[tools]` entirely — the forge produces a nil `ToolHandle`
  and the capability has no inter-capability invocation surface.

#### provider — exported handle kinds (#392, capability-provider only)

```toml
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [
    { name = "device", type = "string", required = true },
    { name = "timeout_ms", type = "int", required = false },
]

[[provider.exports]]
id = "bluetooth.connect"
```

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Canonical handle-kind name. Lowercase, digits, dot, hyphen. Namespace + fingerprint scoping is applied at install time, not in the manifest. |
| `scope_fields` | array of inline-table | no | Typed-field schema the consumer's `[[depends]].scope` table is validated against (#392 Stage 4). Empty / absent means the export takes no scope. |

`scope_fields` entry shape:

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | TOML key the consumer uses in `[[depends]].scope`. Pattern `^[a-z][a-z0-9_]*$` — Go-struct-tag shape, no quoted-keys needed at consumer side. |
| `type` | string | yes | Closed enum: `string` / `int` / `bool` / `string-list` / `int-list`. Anything else is `ErrScopeFieldTypeUnknown`. |
| `required` | bool | no | When `true`, the consumer's `[[depends]].scope` MUST set the field; otherwise `ErrDependsScopeRequiredFieldMissing` at install. Default `false`. |

Constraints:

- Only valid when `kind = "capability-provider"`. Declaring `[[provider.exports]]` on any other kind is a parse-time error (`ErrProviderBlockNotAllowedHere`).
- Empty `[provider]` block (no exports) is allowed on any kind — only declared exports trigger the kind check.
- Duplicate ids in a single block are a parse error.
- Within one export, duplicate `scope_fields[].name` values are a parse error; different exports may share a field name (independent schemas).
- Validation runs **Runtime-side** at consumer install time, against the schema the provider's signed manifest declared (M8 audit finding — a buggy provider implementation cannot accept input broader than declared).

#### depends — declared dependency on registry handles (#392)

```toml
[[depends]]
handle = "alf:fs"

[[depends]]
handle = "alf:tool"

[[depends]]
handle = "abc123:bluetooth.scan"
scope  = { devices = ["my-thermostat"] }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `handle` | string | yes | `<ns>:<id>` — namespace + handle kind. `alf` namespace reserved for daemon-shipped core kinds; otherwise a publisher fingerprint short. |
| `scope` | inline table | no | Opaque parameters consumed by the provider. Validated Runtime-side against the provider's exported scope schema in Stage 4 of `#392`. |

Reserved `alf:` namespace ids (Stage 1 closed allowlist):

```
alf:fs           — filesystem handle (live, see §3.4 fs block)
alf:http         — HTTP client handle (live since #421 Wave 2, see §3.4 http block)
alf:exec         — process exec handle (deferred to 0.9.0+)
alf:secrets      — vault read handle (deferred to 0.9.0+)
alf:events.pub   — event publish handle
alf:events.sub   — event subscribe handle
alf:tool         — inter-capability invocation handle
```

A `[[depends]].handle = "alf:<id>"` reference where `<id>` is not in this set is rejected with `ErrDependsHandleNamespaceReserved`. A non-`alf:` namespace passes format validation in Stage 1; Stage 3 (forge integration) will look up the provider in the runtime registry and fail closed if it's not installed.

Duplicate `handle` values in one manifest are a parse error.

#### raw_imports — escape-hatch direct WASI access (#392)

```toml
[[raw_imports]]
module        = "wasi:clocks/monotonic-clock"
function      = "now"
justification = "high-frequency animation timing — clock granularity below 1 ms required"
```

| Field | Type | Required | Description |
|---|---|---|---|
| `module` | string | yes | WASI Preview 2 interface (e.g. `wasi:clocks/monotonic-clock`). |
| `function` | string | yes | Function name within the interface. |
| `justification` | string | yes | Operator-facing explanation. Surfaced at install. Empty / whitespace-only rejected. |

The classifier is **default-deny**: a module that matches neither the forbidden nor the allowed prefix list is rejected with `ErrRawImportNotInAllowlist`. Adding to either list is a deliberate schema change that updates this doc + the `internal/archtest` pin alongside the code.

**Forbidden prefixes** (must use a scoped handle in `[[depends]]` or the existing `[fs]`/`[events]`/`[tools]` blocks instead):

```
wasi:filesystem/      — host filesystem access; use alf:fs
wasi:sockets/         — TCP/UDP raw access; use a network provider
wasi:random/random    — direct RNG; future alf:crypto handle
wasi:io/streams       — arbitrary fd I/O; use scoped handle
```

**Allowed prefixes** (still surface a warning at install):

```
wasi:clocks/monotonic-clock   — daemon clamps resolution to defeat timing channels
wasi:clocks/wall-clock        — same clamp
wasi:cli/environment          — explicitly scoped env vars per manifest
wasi:cli/exit                 — process exit code
wasi:cli/stdin                — pure compute, no host fs reach
wasi:cli/stdout               — pure compute, no host fs reach
wasi:cli/stderr               — pure compute, no host fs reach
wasi:cli/terminal-input       — tty mode read
wasi:cli/terminal-output      — tty mode write
```

Stage 1 of `#392` validates the manifest at parse time. Stage 4 wires the allowed imports through `internal/runtime/wasm/CheckImports` so the guest can actually link the symbols; until then a guest that imports an allowed-but-not-yet-wired symbol fails at runtime instantiation with `ErrLyingManifest`.

#### http — outbound HTTP allowlist (#421)

```toml
[[http.scopes]]
host = "openlibrary.org"

[[http.scopes]]
host        = "www.googleapis.com"
path_prefix = "/books/v1"

[[http.scopes]]
host = "homelab.local:8443"
```

| Field | Type | Required | Description |
|---|---|---|---|
| `host` | string | yes | Exact hostname. Lowercase (normalised at parse), DNS-shape labels (letters, digits, hyphens), optional `:port` suffix in 1..65535. **No wildcards, no scheme, no path** — those are programmer errors and rejected at parse time. |
| `path_prefix` | string | no | Literal path prefix. Empty means "any path under this host". When set, must start with `/` and contain no glob/regex meta characters (`*`, `?`, `[`, `]`, `\`, `{`, `}`). Matching is segment-aware at the forge layer (`/books/v1` matches `/books/v1` and `/books/v1/X` but NOT `/books/v10`). |

**Status (#421 closed):** Wave 1 (schema + Tier 2 ceiling gate, commit `4653608`) and Wave 2 (host import `alf_http_request` + forge wiring, commit `3846cd4`) are both live. A manifest declaring `[[http.scopes]]` can issue real HTTPS requests at runtime through the scoped `http.Handle`. Egress is routed through the daemon's firewall proxy (`HTTP_PROXY=127.0.0.1:4751`), so per-domain allow/deny rules apply on top of the manifest scope.

**Permission ceiling.** `[[http.scopes]]` widens the trust surface (outbound HTTP) and is therefore **above the Tier 2 (local daemon key) ceiling** per §7.3 of ARCHITECTURE-SECURITY.md. The signer refuses to sign manifests with non-empty `[[http.scopes]]` using the daemon key; the operator must run `alf keygen` + `alf sign` (Tier 3, user-endorsed). The ceiling is re-checked at load time so a manifest that somehow reached the daemon with a Tier-2 signature over an http-scoped manifest fails verification.

**Runtime semantics:**

- Each request URL is matched against the manifest's declared scopes. The first matching scope wins. Out-of-scope requests return `errCode = OUT_OF_SCOPE` to the guest (not a panic — the guest must handle it).
- The runtime forces HTTPS — a guest issuing an `http://` request gets `errCode = TLS_FAILURE` even if the scope's host would otherwise match. The scope schema does not encode the scheme; it is implicitly HTTPS-only.
- Redirects are followed only when the redirect target also matches a declared scope (no cross-host redirect handling in 0.8.x; deferred to 0.10+).
- Request and response bodies are byte-buffered (no streaming in 0.8.x; deferred to 0.10+).
- The `alf_http_request` host import dereferences guest memory only via wazero `api.Memory.Read/Write`, symmetric with `alf_fs_*` (§3.5 of WASM.md).

#### Deferred blocks — parse error in 0.8.0

```toml
# All of the following MUST NOT appear in a 0.8.x manifest.
# The parser rejects them at load time with a pointer to the ticket
# that lands each one.

[[exec.commands]]    # Tier 3.1 exec.Handle,        deferred to 0.9.0+
[[secrets.scopes]]   # Tier 3.1 secrets.Handle,     deferred to 0.9.0+
[memory]             # agent-mediated memory,       deferred to #400
```

**Forward compatibility rule:** a future daemon (0.9.x) that parses a 0.8.0 manifest simply sees an older `alf_envelope_version` and migrates in place. A 0.8.0 daemon that parses a 0.9.0 manifest sees a higher version and fails closed (§3.1).

### 3.5 Signature block (added by signer)

The signature is **not stored inside** `manifest.toml`. A detached envelope lives alongside in the bundle directory:

```
hello-read/
├── manifest.toml
├── manifest.sig          # detached signature envelope (see §7.10)
└── hello-read.wasm       # the binary
```

Details of the `.sig` file format: `ARCHITECTURE-SECURITY.md` §7.10. The signature is computed over the canonicalized `manifest.toml` plus the SHA-256 of the `.wasm` file, pinned by the envelope.

## 4. Per-kind shape

### 4.1 `wasm-tool`

Short-lived capability invoked from the LLM tool loop. Receives a per-call scoped data dir.

```toml
alf_envelope_version = 1

id          = "hello-read"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Hello Read"
description = "Reads a file from the capability's scoped data dir."

[[fs.reads]]
path = "data/"

[[fs.writes]]
path = "data/notes.json"
```

Kind-specific rules:

- `description` is **effectively required**: the LLM tool loop uses it to decide when to invoke.
- At least one of `[[fs.reads]]`, `[[fs.writes]]` is allowed to be absent — a tool that only emits strings (computation-only) is valid.

### 4.2 `wasm-app`

Long-running capability with a UI iframe served from the Control Center.

```toml
alf_envelope_version = 1

id          = "xpost"
kind        = "wasm-app"
version     = "0.4.0"
name        = "XPost"
description = "Draft and schedule Twitter/X posts."

[[fs.reads]]
path = "data/"

[[fs.writes]]
path = "data/"
```

Kind-specific rules:

- `fs.reads` + `fs.writes` are typically both present — the app owns its data dir.

### 4.3 `skill`

Prompt-level capability whose body is a `SKILL.md` co-located with the manifest. Declares the tools the skill is authorised to invoke (#389 Stage 1 shipped).

```toml
alf_envelope_version = 1

id          = "commit-push"
kind        = "skill"
version     = "1.2.0"
name        = "Commit and push"
description = "Stages, commits with a generated message, pushes."

[[tools.declares]]
id = "bash"
```

Bundle layout:

```
commit-push/
├── manifest.toml         # signed envelope (this file)
├── manifest.sig          # detached signature; daemon-signed at first boot if absent
└── SKILL.md              # prompt body (the bundle hashed into the trusted comment)
```

Kind-specific rules:

- `SKILL.md` IS the bundle. The signature pipeline hashes its bytes into
  the trusted comment per §7.10; tampering after signing is rejected at load.
- Discovery metadata — `triggers`, `tier` — stays in `SKILL.md` YAML
  frontmatter on purpose. Those fields drive *when* a skill surfaces,
  not *what* it can do, and they live outside the security envelope by
  design. Editing `SKILL.md` (frontmatter or body) invalidates the
  signature; on a daemon-key bundle the next boot re-signs.
- `[[tools.declares]]` is the cap surface. The forge mints a single
  `ToolHandle` scoped to the listed ids; tools not in the block are
  absent from the LLM tool menu (Stage 2 wires the filter through the
  orchestrator).
- No `[memory]` block. Memory is Tier 3.2 (agent-mediated, #400) — no
  structural handle even when a skill needs it; the skill calls
  agent-callable memory tools the LLM gates with the kernel prompt.

### 4.4 `capability-provider` (#392)

Bundle that exports new handle kinds into the runtime registry. Dependent
capabilities reference exported kinds via `[[depends]].handle = "<ns>:<id>"`
where `<ns>` is the publisher's fingerprint short. Stage 1 of `#392` ships
the manifest schema; Stage 3 wires the runtime forge.

```toml
alf_envelope_version = 1

id          = "alf-bluetooth-provider"
kind        = "capability-provider"
version     = "0.1.0"
name        = "Bluetooth Provider"
description = "Exports bluetooth.scan and bluetooth.connect handle kinds."

[[provider.exports]]
id = "bluetooth.scan"

[[provider.exports]]
id = "bluetooth.connect"
```

### 4.5 `llm-provider`

LLM backend bundle (Anthropic, OpenAI, Ollama, …). Reserved; no new LLM
provider bundles in 0.8.0 beyond the maintainer-signed ones that ship with
the daemon. The `[provider]` block is rejected on this kind — exporting
handle kinds is the capability-provider role (§4.4).

```toml
alf_envelope_version = 1

id          = "claude"
kind        = "llm-provider"
version     = "1.0.0"
name        = "Claude"
description = "Anthropic Claude provider."
```

### 4.6 `marketplace-app` (deprecated)

Identical shape to `wasm-app` for 0.8.0 compatibility. Migration of all existing `marketplace-app` manifests to `wasm-app` is tracked in the migration section of `ARCHITECTURE-SECURITY.md` §12.

## 5. Permission ceiling

Not every signer can endorse every permission set. The ceiling is enforced at **sign time** (the signer refuses to produce the signature) AND at **verify time** (the verifier re-checks the ceiling — belt-and-braces per `ARCHITECTURE-SECURITY.md` §7.4 state diagram).

### 5.1 Local-daemon-key ceiling (tier 2)

A bundle signed by the auto-generated local-daemon key is allowed to declare:

| Block | Allowed |
|---|---|
| `[[fs.reads]]` | yes, bounded to the capability's own bundle dir + data dir |
| `[[fs.writes]]` | yes, bounded to the capability's own data dir |
| `[[http.scopes]]` | **no** — widening requires user-endorsed key |
| `[[exec.commands]]` | **no** |
| `[[secrets.scopes]]` | **no** |
| `[memory]` | agent-mediated only (no scoped handle) |
| `[[events.exports]]` | own topics only |
| `[[events.subscribes]]` | requires matching `[[events.exports]]` in producer manifest (via cross-capability flow declaration in `#399`) |

A manifest signed by the local-daemon key that declares anything outside the ceiling fails verification at `ARCHITECTURE-SECURITY.md` §7.4 step "tier-2 ceiling check".

### 5.2 User-endorsed-key ceiling (tier 3)

No ceiling. The user explicitly endorsed the key and sees the declared permissions at sign time (the `alf sign` prompt lists them before proceeding).

### 5.3 Third-party-key ceiling (tier 4)

No ceiling at sign time (the third-party signer is outside alf's reach). Each install of a bundle signed by a third-party key is subject to per-install ratification (§6.3 admin boundary) — the operator sees the declared permissions before approving.

## 6. Unknown-field handling

**Fail closed.** A parser that encounters an unknown top-level key, an unknown sub-table, or an unknown field in a known table **rejects the manifest** and the verify flow aborts (step 10 of `ARCHITECTURE-SECURITY.md` §7.4).

Rationale: forward compatibility at the cost of security (accept-unknown) is the exact failure mode that made SAML/JWT/PKCS#7 parser divergence into real CVEs. We bump `alf_envelope_version` for every additive change so old daemons explicitly refuse new manifests instead of silently ignoring the new fields.

## 7. Canonicalization (summary)

Full procedure in `ARCHITECTURE-SECURITY.md` §7.10. In short:

1. Parse the `manifest.toml` bytes with the pinned `pelletier/go-toml/v2`.
2. Validate against this schema (all required fields, no unknown fields, no deferred blocks in 0.8.0).
3. Project to a canonical JSON tree — alphabetical keys, explicit nulls instead of absent optionals, arrays of tables preserved as JSON arrays of objects, TOML date/time serialized as RFC 3339 strings.
4. Serialize via RFC 8785 JSON Canonicalization Scheme (JCS) to deterministic bytes.
5. Those bytes are what the signature covers.

Property tested by `#397`'s reference implementation: two TOML files with the same logical content but different byte formatting produce byte-identical canonical output.

## 8. Migration from `manifest.json` (0.7.x → 0.8.0)

The tooling ticket (TBD, opened when `#397`'s reference implementation lands) will deliver:

- `alf migrate manifests` — scans `apps/*/manifest.json` + `skills.d/*/manifest.json`, produces `manifest.toml` at each path, preserves semantics, leaves `.json` as `.json.pre-0.8.0.bak` for rollback reference.
- Idempotency: running the migration twice is a no-op.
- A manifest that does not map cleanly (unrecognised fields, custom extensions) is flagged in a report; operator resolves manually.

## 9. Authoring examples

### Minimal `wasm-tool` (no filesystem access)

```toml
alf_envelope_version = 1
id          = "stringify"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Stringify"
description = "Returns its input, verbatim."
```

Valid. Capability runs with zero handles; the LLM tool loop can only call it with inputs and receive strings back.

### `wasm-tool` with read-only config

```toml
alf_envelope_version = 1
id          = "linter"
kind        = "wasm-tool"
version     = "0.2.0"
name        = "Linter"
description = "Lints code against a declarative config."

[[fs.reads]]
path = "config.toml"

[[fs.reads]]
path = "rules/"
```

### Invalid — declares `exec` in 0.8.0

```toml
alf_envelope_version = 1
id          = "runner"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Runner"

[[exec.commands]]   # ← parse error: [[exec.commands]] deferred to 0.9.0+
path = "/usr/bin/git"
```

Verify flow rejects with an explicit error naming the block and the ticket that will land it. `[[http.scopes]]` is **valid** in 0.8.0 (#421 Wave 1+2) but requires Tier-3 user-endorsed signing — see §3.4 http for the workflow.

### Invalid — unknown top-level field

```toml
alf_envelope_version = 1
id          = "widget"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Widget"
author      = "Jane Doe"   # ← not in schema, rejected
```

The spec has no `author` field. A future version might add one, but it requires an `alf_envelope_version` bump — not silent acceptance.

## 10. References

- `docs/ARCHITECTURE-SECURITY.md` §7 (trust & vault), §7.10 (envelope & canonicalization), §7.4 (install/load state diagram)
- `docs/WASM.md` §5 (manifest authoring guide for WASM bundles specifically)
- [TOML 1.0 spec](https://toml.io/en/v1.0.0)
- [RFC 8785 — JSON Canonicalization Scheme](https://datatracker.ietf.org/doc/html/rfc8785)
- Tickets: `#387` (trust model), `#388` (load-time verify), `#391` (ocap forge), `#397` (this spec + reference implementation)
