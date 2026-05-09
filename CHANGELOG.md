# Changelog

All notable changes to ALF are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Older releases (0.7.8 and earlier) are not retroactively documented here —
see the Git history and GitHub releases for pre-0.7.9 changes.

---

## [Unreleased] — 0.8.0 development

In-progress work on `release/0.8.0`. Items here have landed on the branch
but no v0.8.0 tag has been cut. See
[`docs/ARCHITECTURE-SECURITY.md`](docs/ARCHITECTURE-SECURITY.md) §12 for
the milestone ticket map.

### Added

- **#402 — Update permission-widening goes through ratification**.
  `Manager.Update` now diffs the new manifest's declared
  permissions against the previous install's set and refuses any
  widening that has not been explicitly approved by an operator
  via the admin pending queue (#395 Stage 2). Closes the v0.7.9
  audit finding "manifest update silently widens perms".

  **Why this matters.** Pre-#402 `Update` replaced
  `m.perms[slug]` with whatever the new manifest declared.
  Scenario: install-time perms = `["storage"]` (user-approved);
  update-time perms = `["storage", "bash", "network"]` — no
  prompt, no log, the LLM-built capability surface widens
  silently the next time the supervisor restarts the app. Under
  the v0.8.0 trust model (signed manifests + admin boundary)
  this is a privilege-escalation path that has to be closed
  before tag-time.

  **Order of operations** in the new `Update`:
  1. Download + verify the bundle (envelope.Verify, #384).
  2. Read the NEW `manifest.json` from the in-memory ZIP via
     `readManifestJSONFromZip` — bounded by
     `MaxBundleManifestJSONSize = 16 KiB`.
  3. Read the OLD `manifest.json` off disk (still present —
     deactivate hasn't fired yet).
  4. `diffPermissions(prev, next)` returns the sorted list of
     perms in next but absent from prev. Set-semantics —
     duplicates collapse, ordering is irrelevant.
  5. If widening:
     - `permRatifier` wired → enqueue a `KindPermissionWiden`
       item, return `ErrPermissionWideningPending` with the
       queue ID embedded in the message.
     - `permRatifier` nil → return
       `ErrPermissionWideningRefused`. No fallback to silent
       widening.
     In both cases, NO on-disk state changes; the running app
     keeps its old version.
  6. If narrowing or unchanged: deactivate, wipe, extract,
     re-activate (legacy flow).

  **`internal/marketplace/permdiff.go`.** New file carrying:
  - `PermissionRatifier` callback type — the daemon's seam to
    the admin pending queue. Returns the assigned queue ID and
    any enqueue error.
  - `ErrPermissionWideningPending` (widening + ratifier wired,
    queued for approval).
  - `ErrPermissionWideningRefused` (widening + no ratifier —
    "refusing to silently widen").
  - `diffPermissions(prev, next) []string` — set-difference
    over string slices, alphabetically sorted, never-nil.
  - `Manager.SetPermissionRatifier(fn)` setter for the daemon
    to wire the queue.

  **`internal/marketplace/bundle.go`.** Extended with
  `readManifestJSONFromZip` (mirrors `readManifestFromZip` but
  for the legacy JSON view) + `ErrBundleManifestJSONMissing`
  for the typed-error path. The TOML envelope (manifest.toml)
  is the trust gate; manifest.json is the runtime metadata
  source the activate path consumes — until #414 retires that
  legacy step.

  **`internal/marketplace/manager.go` refactor.** Split the
  monolithic `downloadAndExtractBundle` into:
  - `downloadAndVerifyBundle(slug) ([]byte, error)` — fetch
    bundle.zip + bundle.sig, verify, return bundle bytes.
  - `extractVerifiedBundle(slug, appDir, bytes) error` — write
    to disk; caller has already verified.
  - `downloadAndExtractBundle` is now a convenience wrapper
    Install consumes. Update uses the split helpers so it can
    interpose the permission-widening gate between verify and
    extract.

  **Daemon wiring.** `cmd/alf-daemon/main.go` constructs a
  `pending.NewDirStore(<dataDir>/admin/pending/, time.Now)` at
  marketplace init time and wires a closure into
  `mpManager.SetPermissionRatifier`. The closure calls
  `pendingStore.Append(ctx, pending.Item{Kind:
  KindPermissionWiden, Payload: { slug, old_perms, new_perms,
  added_perms }})` and returns the assigned queue ID. When
  `pending.NewDirStore` itself fails (perms / disk full),
  `SetPermissionRatifier` is NOT called — Update widenings
  then refuse outright per the design.

  **Operator UX.** A widening Update surfaces with:
  ```
  marketplace: update permissions widened — operator ratification
  required: queue id=000000000042 app="myapp" added=[bash network]
  — run `alf ratify 000000000042` to approve
  ```
  After `alf ratify`, the operator re-runs `alf install myapp`
  (or `Update` once an automated retry path is wired) to
  proceed.

  **Acceptance covered.**
  - ✅ Update path diffs old vs new permissions BEFORE
    committing (`TestUpdate_WideningWithRatifierEnqueues`
    asserts m.perms[slug] is unchanged AND on-disk
    manifest.json is unchanged after a refused-pending
    Update).
  - ✅ Widening requires explicit user approval through the
    admin boundary (#395) — not the LLM. The `permRatifier`
    callback is the only path; there is no LLM-reachable bypass.
  - ✅ Narrowing is allowed silently
    (`TestUpdate_NarrowingProceedsSilently` verifies the
    ratifier is NOT called when next ⊆ prev).
  - ✅ Refused-pending Update does not reach m.perms[slug]
    (asserted in the integration test).

  **Archtest exception (`TestOneVerifyCallSite`).**
  `internal/marketplace/bundle.go` is now an explicitly-listed
  caller of `envelope.Verify`. Marketplace-app is deprecated
  (see MANIFEST-SCHEMA §4.6) and runs a binary daemon, not the
  wazero forge — `InstantiateVerified` would mint a handle
  Instance the marketplace doesn't need. The caller still
  hits the same `envelope.Verify` pipeline (full, not a
  lower-level primitive), so the security property is
  preserved. Listed explicitly so a future move to `wasm-app`
  routing through Instantiator is a deliberate archtest update,
  not silent drift.

  **Tests.** 8 new permdiff tests in `permdiff_test.go`
  (empty/nil edges / narrowing-silent / widening-surfaces-added /
  order-insensitive / duplicates-collapse / empty-prev-any-next /
  err-sentinels-distinct), 4 new Update integration tests in
  `update_widen_test.go` (narrowing-proceeds /
  widening-with-ratifier-enqueues / widening-without-ratifier-
  refused / ratifier-enqueue-error-propagates). 12 new tests
  total. Race detector clean.

- **#384 — marketplace bundle signing (Layer 2 distribution)**.
  Closes the 0.7.9 critical "marketplace integrity" finding by
  routing every marketplace install through the same
  `envelope.Verify` pipeline used for WASM tools and skills (#388).
  No special-case for marketplace; one verify path per the §2.2
  architecture.

  **Why this matters.** Pre-#384 `Manager.Install` set
  `m.trusted[slug] = true` on the sole basis that the ZIP came
  from the configured `ALF_MARKETPLACE_URL`. Once wazero loads
  third-party code in-process (#386), the chroot blast-radius
  cap is gone — unsigned bundle execution is qualitatively worse
  than the v0.7.x model. Bundle provenance MUST chain to a
  trusted publisher key before the daemon writes a single file
  to disk.

  **Wire contract** (the marketplace server will need a parallel
  upgrade):
  - `GET <registry>/api/apps/<slug>/bundle?arch=<arch>` →
    bundle.zip (ZIP must contain a top-level `manifest.toml`
    envelope of `kind = "marketplace-app"` or `"wasm-app"`).
  - `GET <registry>/api/apps/<slug>/bundle.sig?arch=<arch>` →
    detached minisign signature over the canonicalised
    manifest.toml, with the bundle's SHA-256 hash embedded in
    the trusted comment.
  - 404 on `bundle.sig` surfaces as `ErrBundleSignatureMissing`
    so operators see "registry has not yet been upgraded for
    v0.8.0 signed bundles" rather than a confusing transport
    error.

  **`internal/marketplace/bundle.go`.** New file carrying:
  - `readManifestFromZip([]byte) ([]byte, error)` — extracts
    `manifest.toml` from the in-memory ZIP without writing to
    disk; bounded by `MaxBundleManifestSize = 64 KiB` against
    memory bombs.
  - `verifyBundle(bundle, sig, store)` — the marketplace seam
    over `envelope.Verify`. On success, returns the parsed
    manifest with kind discriminator preserved. Refuses any
    kind other than `marketplace-app` / `wasm-app` (the two
    share the same envelope shape per MANIFEST-SCHEMA §4.6).
  - Typed errors: `ErrBundleManifestMissing`,
    `ErrBundleSignatureMissing`,
    `ErrBundleManifestNotMarketplace`. Operators branch on
    these to surface the precise failure cause.

  **`Manager.SetTrustStore` + verify integration.** New setter
  wires the daemon's shared `envelope.TrustStore` (same set of
  keys as the WASM loader, skills loader, and CRL refresher)
  into the manager. `Install` and `Update` both refuse to run
  if it's nil — there is no fallback to the pre-#384 unsigned
  path. `downloadAndExtractBundle` now downloads bundle.zip +
  bundle.sig in parallel, calls `verifyBundle` BEFORE any disk
  write, and only then extracts via `extractBundle`. The 200
  MiB bundle ceiling carries over; signature is bounded to
  `MaxBundleSignatureSize = 4 KiB`.

  **Trust-flag heuristic removed.** `m.trusted[slug] = true` is
  no longer set by `Install`. The `MarkTrusted` API is reserved
  for built-in apps (e.g. the `developer` mode app). Marketplace
  installs are untrusted; the signer-chain check is the install
  gate, the per-tier permission ceiling (Tier-2 daemon-key vs
  Tier-3 user-endorsed) is independent — see MANIFEST-SCHEMA §5.

  **`envelope.MarketplacePublicKey`.** New `go:embed`-backed
  pubkey at `internal/capability/envelope/marketplace_pubkey.minisign`
  mirroring the release-key pattern. On a fresh checkout the
  file is empty; `MarketplacePublicKey()` returns
  `ErrNoMarketplaceKey` and the daemon logs once at boot
  ("no marketplace pubkey embedded — installs require operator-
  imported third-party keys"). When populated, the daemon
  auto-adds the key to `wasmRt.TrustStore` so signed bundles
  from alf-marketplace flow without operator action.

  **Legacy code retired.** `installLegacy`, `downloadFile`,
  `downloadBinary`, `downloadSkills`, `downloadWebAsset`, and
  `httpGet` deleted. They formed a dead-code subgraph rooted
  at the per-file fallback that pre-dated bundle ZIPs. Update
  also drops its bundle-then-legacy fallback chain.

  **TLS-pinned registry — note.** `ALF_MARKETPLACE_URL` already
  enforces HTTPS (rejected at NewManager construction unless
  `ALF_MARKETPLACE_INSECURE=1`). TLS certificate pinning beyond
  the system CA bundle is deferred — the marketplace pubkey
  embed is the trust anchor for bundle authenticity; transport-
  level pinning would harden against a CA mis-issuance scenario
  but does not gate authority. Will land alongside the homelab
  marketplace deployment once the cert hash is stable.

  **Acceptance covered.**
  - ✅ Unsigned bundle cannot be installed
    (`TestInstall_RejectsUnsignedBundle`).
  - ✅ Bundle signed with unknown key cannot be installed
    (`TestInstall_RejectsUnknownPublisher`,
    `envelope.ErrSignerNotTrusted`).
  - ✅ Bundle modified after signing rejected
    (`TestInstall_RejectsTamperedBundle` flips a byte at offset
    60).
  - ✅ Plain-HTTP registry refused at construction
    (`validateRegistryURL`, pre-existing).
  - ✅ `Update()` re-verifies signature: `Update` calls the same
    `downloadAndExtractBundle` so every update transits the
    verify pipeline.
  - ✅ Permission widening through ratification: tracked under
    #402, immediate follow-up.

  **Server-side dependency.** The marketplace server
  (`alf-marketplace` repo) will need to:
  - Generate the marketplace keypair; ship the pubkey for embed.
  - Author a `manifest.toml` envelope per app at publish time.
  - Sign canonicalised manifest.toml + bundle.zip hash → produce
    bundle.sig.
  - Serve `bundle.sig` at the URL above.

  Until that ships, marketplace installs error out with a clear
  message pointing at `ErrBundleSignatureMissing`. No dev-mode
  bypass — the security guarantees are stronger as a hard gate
  than a flag.

  **Tests.** 7 new bundle-verify tests in `bundle_verify_test.go`
  (happy-path / wasm-app kind / wrong kind / tampered bundle /
  unknown key / missing manifest.toml / oversized manifest), 5
  new manager-flow tests in `manager_registry_test.go` (happy
  signed install / unsigned-bundle refusal / unknown-publisher
  refusal / tampered-bundle refusal / no-trust-store-wired
  refusal), 1 new envelope test for the empty-embed dev-build
  path (`MarketplacePublicKey`). Existing manager tests
  (`TestUpdate_PreservesDataDir`) updated to use the signed
  fixture. 13 new tests total. Race detector clean.

- **#396 deliverable 2 — provider revocation cascade discovery
  channels**. The cascade engine (`Instantiator.RevokeByKey`,
  shipped in #392 Stage 5) closes every Instance signed by — or
  depending on — a revoked key, but the daemon had no live
  observation of the trust store: an operator running
  `alf trust revoke <fp>` had to follow with `alf restart` for
  the cascade to actually fire, and a CRL-published revocation
  applied only at the next daemon boot. This drop closes both
  loops.

  **Why this matters.** D8 (`alf trust revoke` CLI) shipped in
  #395 Stage 2 chunk 1, but the comment trail explicitly noted
  that `alf restart` was the required operator workflow because
  the daemon did not pick up new `.revoked` sidecars at runtime.
  D2 had been blocked on #392 (provider cascade machinery), which
  Stage 5 of that ticket landed last week. With both pieces in
  place, the only remaining work was wiring discovery → cascade.

  **`MemoryTrustStore.AllRevoked()`.** New snapshot method that
  returns a fresh copy of every revoked key with its strictest
  (operator-set + CRL-set merged via the same earliest-wins rule
  as `RevokedAfter`) not-valid-after timestamp. The returned map
  is safe to mutate or retain — the cascader's diff logic does
  exactly that across reloads. Companion to `RevokedAfter` for
  bulk consumers.

  **`runtime.RevocationCascader`.** New type in
  `internal/runtime/cascade.go` that diffs revoked-set snapshots
  and calls `Instantiator.RevokeByKey` for keys whose state
  crossed one of two transitions: newly revoked (not in previous
  snapshot, present now) or tightened (previously revoked at T1,
  now revoked at T0 < T1 — the operator override for "compromise
  actually started earlier"). The first snapshot is taken
  eagerly at `NewRevocationCascader` time so the boot baseline
  doesn't fire spurious cascades — keys revoked at boot don't
  cascade because their bundles never made it past
  `envelope.Verify` in the first place. Refresh is mutex-
  serialised so a SIGHUP-driven call and a CRL OnApply-driven
  call cannot race on the diff.

  **`crl.Refresher.OnApply`.** New optional callback field that
  fires AFTER each successful `Store.ApplyCRL` (both source and
  cache paths). Daemon points it at the cascader so a CRL-
  published revocation closes live Instances within the same
  Tick. Source-failure and malformed-source paths do NOT fire
  the callback (nothing was applied — cascading would be wrong).
  Nil OnApply is a no-op for backward compatibility.

  **SIGHUP handler in `cmd/alf-daemon/revocation_cascade.go`.**
  `setupRevocationCascade` constructs the cascader against
  `wasmRt.Inst` + `wasmRt.TrustStore.AllRevoked`, registers a
  SIGHUP subscriber, and returns a void-shaped onApply callback
  for `setupCRL` to plug into the Refresher. The handler
  goroutine exits on context cancellation — daemon shutdown
  stops it cleanly. On SIGHUP: `TrustStore.Load()` re-reads the
  dir (operator-set `.revoked` sidecars surface in the in-memory
  map), then `cascader.Refresh()` cascades for every newly-
  revoked key. Audit line per SIGHUP: `[cascade] SIGHUP reload:
  trust dir=… revoked=N cascaded=M`. One-line transition entries
  per cascaded key: `[cascade] key newly revoked: <fp>
  not-valid-after=…` or `[cascade] key revocation tightened:
  <fp> not-valid-after=… (was …)`.

  **Operator workflow after this drop.** `alf trust revoke <fp>`
  writes the `.revoked` sidecar; `kill -HUP <daemon-pid>` (or
  `alf restart` for the conservative path) is now sufficient —
  no restart needed to cascade. CRL-published revocations
  cascade automatically on the next Tick (≤ 6h by default, no
  operator action). Both channels share the same audit format
  so the operator can diff `journalctl -u alf` against the
  revocation event.

  **What's NOT in this drop.** fsnotify-based directory
  watching is deferred — the SIGHUP path is sufficient for the
  v0.8.0 single-host scale and matches the convention already
  documented in `truststore.go` ("the daemon reloads on SIGHUP /
  CC action, not on every Lookup"). A fsnotify upgrade lands
  alongside the CC ratification page (#395 follow-up) so the
  same channel covers UI-driven trust mutations too.

  **Tests.** 3 new envelope tests in `crl_test.go`
  (`AllRevoked` merge / fresh-copy / empty), 6 new runtime
  cascader tests in `cascade_revocation_test.go` (newly-revoked
  cascades, tightened-boundary cascades, boot-baseline doesn't
  cascade, softened doesn't cascade, concurrent Refresh
  serialised, nil-logf no-ops), 5 new CRL Refresher tests
  (`OnApply` source / cache / source-failure-skips /
  malformed-skips / nil-no-ops), and 4 new daemon tests in
  `revocation_cascade_test.go` (nil-wasmRt returns nil,
  happy-path wires cascader + onApply, SIGHUP triggers
  reload+cascade, context-cancel stops handler). 18 new tests
  total. Race detector clean.

  **#396 status after this drop.** Deliverable 2 closed.
  Deliverable 8 transitions from "shipped, requires `alf
  restart`" to fully end-to-end (sidecar → SIGHUP → cascade in
  the same daemon). The §8 revocation pipeline is now complete:
  bundle signing → trust-store verify → operator-set Revoke
  OR upstream CRL → ApplyCRL → cascade discovery → RevokeByKey
  → Instance.Close in <200ms. Closing #396.

- **#392 Stage 5 — provider revocation cascade**. Stages 1-4 shipped
  the schema, registry, forge integration, and scope validation.
  Stage 5 closes the §3.1 security promise that revoking a provider's
  trust store entry actually unwinds every consumer that depended on
  it — not just the bundles signed by the revoked key.

  **Why this matters.** Without cascade, revoking a compromised
  provider key would close the provider's own Instance but leave
  consumer Instances live, still calling into the (now-detached)
  registry entry. The handle would still resolve via Lookup until
  the consumer's own Close fired — meaning a revoked provider could
  keep serving authority through cached references. Cascade closes
  every dependent consumer in the same revocation event, so the
  authority loss is atomic.

  **`envelope.KeyIDFromHex(s)`.** New helper that parses the
  16-char hex form back into a `KeyID`. The runtime cascade needs
  this to convert `[[depends]].handle` namespace strings (which
  carry the publisher's lowercase fingerprint as Stage 3 wired)
  into the trust-store identity. Accepts both upper and lower case
  for round-trip compatibility with `KeyID.Hex()`. Returns
  `ErrKeyIDInvalidHex` on length mismatch or non-hex chars.
  Defensive: the runtime cascade silently skips namespaces that
  fail this parse — envelope.Validate is the authoritative format
  gate, so a manifest reaching the runtime with a non-hex namespace
  is a defensive corner case (e.g. a future schema change that
  loosens the format), not an attack surface.

  **`runtime.dependsOnKeys(*envelope.Manifest)`.** Pure helper that
  walks `manifest.Depends` and collects every distinct provider
  KeyID:
  - `alf:` namespace excluded — alf core kinds are not
    provider-keyed and never get revoked via the cascade path.
  - Duplicates collapsed (a manifest depending on the same
    provider for two different handles only tracks the key once).
  - Non-hex namespaces silently skipped.

  Returns `nil` for the common case (skill / wasm-tool bundles
  that only depend on `alf:*` core kinds).

  **`liveEntry.dependsOn` field.** Added to the per-Instance
  tracking record. Populated at `trackLive` time from
  `dependsOnKeys(vm.Manifest)`. `RevokeByKey` walks the live
  registry and closes both:
  - Instances whose `signerID == revokedKey` (existing direct path)
  - Instances whose `dependsOn` contains `revokedKey` (new cascade
    path)

  **Audit logger surfaces close reasons.** Each closed Instance
  emits one log line via `revocationLogger`. Two distinct reason
  strings:
  - `signed by revoked key <fp>` — direct revocation
  - `depends on revoked provider key <fp>` — cascade revocation

  An operator can tell the two apart at a glance: a single
  revocation that affects 5 caps will show 1 direct + 4 cascade
  lines, exactly mapping the dependency graph.

  **Tests.** 4 new runtime tests in `cascade_test.go`:
  - `RevokeByKey_DirectSignerOnly` — legacy path, no cascade
    involved (Instance has no provider depends), reason text is
    "signed by revoked key"
  - `RevokeByKey_CascadeCloseDependentConsumer` — load-bearing
    Stage 5 invariant: provider + consumer signed by different
    keys, RevokeByKey on provider's key closes BOTH; audit log
    carries both direct + cascade lines
  - `RevokeByKey_NoCascadeForUnrelatedConsumer` — alf-fs-only
    consumer is untouched by an unrelated key revocation
    (LiveCount stays 1)
  - `DependsOnKeys_PureFunction` — table-driven pin: no depends,
    alf only, single provider, two providers, duplicate provider
    collapsed, mixed alf+provider

  29 archtests still green; race detector clean.

  **What's deferred.** Two original Stage 5 items pushed to
  follow-ups:
  - **`alf provider list/install/remove` CLI**: open design
    questions around the Docker host/container boundary (where
    provider bundles live on the host vs in the daemon's mount),
    and 0.8.0 ships zero capability-provider bundles, so the CLI
    surface has no consumers yet. Will follow up with an explicit
    ticket once the bundle-distribution channel and example
    providers exist.
  - **`alf trust revoke <fp>` → daemon RevokeByKey hookup**:
    deferred to #396 Stage 2 deliverable 8. The existing
    `alf trust revoke` writes a `.revoked` sidecar; the running
    daemon would need to discover the change and call
    `RevokeByKey`. The cascade machinery is now ready for that
    wiring whenever the loop lands.

  **Stage 5 closes the technical work for #392.** All four
  acceptance criteria from the load-bearing security promise are
  now covered:
  - [x] `[[depends]]` fails closed on unregistered handle (Stage 3)
  - [x] Installing the provider then loading the consumer succeeds (Stage 3)
  - [x] Two providers same id distinguished by fingerprint (Stage 3)
  - [x] Scope validation runs Runtime-side (Stage 4 — M8 audit finding)
  - [x] Provider revocation cascades to children (Stage 5)

  Operational surface (CLI + transitive trust display + raw-imports
  install prompt) lives with follow-ups; the security architecture
  is structurally complete.

- **#392 Stage 4 — scope schema validation (M8 audit finding)**.
  Stages 1-3 wired the `[[depends]]` namespace + handle resolution.
  Stage 4 closes the M8 audit finding: per-handle scope schemas
  declared by the provider, validated Runtime-side against the
  consumer's `[[depends]].scope` table at forge time. A buggy
  provider implementation can no longer accept input broader than
  its manifest declared — the runtime does the type-checking, the
  guest never sees scope until validation passes.

  **`[[provider.exports]].scope_fields`.** New optional array on
  each export. Each entry is `{name, type, required}` where:
  - `name` matches `^[a-z][a-z0-9_]*$` (Go-struct-tag shape — no
    quoted-keys needed at the consumer side)
  - `type` is one of a closed enum: `string`, `int`, `bool`,
    `string-list`, `int-list`. Anything else is
    `ErrScopeFieldTypeUnknown`. Adding a new type requires a
    runtime branch in `checkScopeValueType` AND a doc update.
  - `required` toggles whether the consumer must declare the field

  Within one export, duplicate `name` values are a parse error;
  different exports may share a field name (independent schemas).

  JSON Schema was rejected as over-engineering. The actual catalogue
  in #392 (Bluetooth device names, GPU device IDs, IoT topic IDs,
  HTTP scope hosts) is flat key/value with primitive types — a
  closed-enum field-list covers it without a schema-validator
  dependency. If a future need arises for nested objects or regex
  patterns, the type enum can grow.

  **`handle.HandleKind.ScopeFields`.** New field on the registry
  entry, populated by `RegisterProviderExports` at install time.
  `alf:*` core kinds carry `nil` (they're forged from the legacy
  `[fs]`/`[events]`/`[tools]` blocks, not via the registry's
  scope-validation path). The runtime registry uses its own
  `handle.ScopeField` type rather than re-importing
  `envelope.ScopeField` — preserves the one-way dependency
  (runtime → envelope, never the reverse).

  **`(*Instantiator).resolveDepends` extension.** After the
  registry Lookup hit, `validateScopeAgainstSchema` runs the
  type checks. Failure paths:
  - `ErrDependsScopeRequiredFieldMissing` — required field not in scope
  - `ErrDependsScopeUnknownField` — scope key has no matching schema entry
  - `ErrDependsScopeFieldTypeMismatch` — value type wrong (e.g. int declared, string supplied)
  - `ErrDependsScopeNonEmptyButNoSchema` — provider declares no fields but consumer passes scope (function with no parameters)

  Type checks accommodate TOML decoder semantics: `int64` / `int` /
  `int32` for `int`, `[]any` for lists with per-element type checks.
  The forge runs only after every depends entry validates; failure
  surfaces before any handle is created.

  **`envelope.translateScopeFields` private helper** in
  `internal/runtime/instantiator.go` converts
  `[]envelope.ScopeField` to `[]handle.ScopeField` at the package
  boundary — the duplication keeps the `handle` package free of
  envelope imports.

  **What's NOT in Stage 4.** Two original Stage 4 items deferred:
  - **Raw-imports `CheckImports` pass-through**: today's daemon
    runs WASI Preview 1 (`wasi_snapshot_preview1` unconditionally
    allowed for the Go runtime). The Stage 1 `[[raw_imports]]`
    declarations use Preview 2 syntax (`wasi:clocks/...`) which
    doesn't map to Preview 1 module names. The forward-looking
    schema is in place; runtime wiring waits for Preview 2 in
    wazero — tracked as a follow-up.
  - **Transitive trust display**: install-UX, naturally lives with
    the CLI work in Stage 5.

  **Tests.** 13 new envelope tests in `provider_schema_test.go`:
  scope_fields happy path with all 5 types, absent yields nil,
  every error sentinel (`NameEmpty`, `NameMalformed`, `TypeEmpty`,
  `TypeUnknown`, `Duplicate`), per-export name isolation. 14 new
  runtime tests in `scope_validate_test.go`: validator unit tests
  (both/empty, non-empty/no-schema, required-missing, optional-absent,
  unknown-field, type checks for each type via table-driven, multiple
  required reports first); integration tests through full
  verify→register→load flow (happy path, required-missing,
  type-mismatch, unknown-field, scope-for-fieldless-provider,
  alf-core no-scope accepted, alf-core scope-passed rejected).

  All 29 archtests still green; race detector clean.

- **#392 Stage 3 — forge integration: depends resolution + provider
  exports registration**. Stage 1 shipped the schema, Stage 2 shipped
  the runtime registry. Stage 3 wires them through the verify+forge
  path so consumer manifests' `[[depends]]` entries actually resolve
  against the registry, and capability-provider bundles populate it
  with their `[[provider.exports]]` at install time. The original
  ticket called for a separate `internal/runtime/providers/manager.go`
  package; that's replaced by direct integration into
  `InstantiateVerified` so the registry mutation goes through the
  one verify path the codebase already pins via `TestOneVerifyCallSite`
  (#388 deliverable 2).

  **`KeyID.HexLower()`.** New method on `envelope.KeyID` returning
  the 16-char lowercase hex form. Manifest-syntax references in
  `[[depends]]` use lowercase per the schema's `dependsHandlePattern`
  (Stage 1) — uppercase `KeyID.Hex()` wouldn't round-trip. The full
  16 hex chars are kept as the "fingerprint short" (64 bits, ~280
  trillion combinations — collision-resistant enough that
  truncation is unnecessary). Picking "no truncation" once means the
  N parameter from the §H2 spec note is documented and stable; any
  future change would invalidate every shipped reference.

  **Instantiator API.** Three additions:
  - `WithHandleRegistry(*HandleRegistry)` option — stores the
    registry on the Instantiator. When set, `InstantiateVerified`
    validates `[[depends]]` and registers capability-provider
    exports through it. When omitted (tests + legacy callers),
    both steps are skipped — matches the "no registry, no
    authority" precedent of `WithEventsBus` / `WithToolInvoker`.
  - `RegisterProviderExports(reg, signerID, exports)` — sibling to
    `SeedHandleRegistry`. Iterates exports and registers each as
    `HandleKind{Namespace: signerID.HexLower(), ID: e.ID}`. Token
    never escapes the Instantiator. Empty exports → no-op.
    Returns the registry's first error on duplicate; partial state
    is intentionally not rolled back (the caller treats install
    as one transaction).
  - Helper `(*Instantiator).resolveDepends(*envelope.Manifest)` —
    private, called by `InstantiateVerified` BEFORE forge. Walks
    `manifest.Depends`, splits each `<ns>:<id>` via the new
    `envelope.DependsEntry.SplitHandle()`, looks up in the
    registry. First miss returns
    `ErrDependsHandleNotRegistered` wrapped with the offending
    handle reference. The §3.1 ocap promise holds: a guest with
    an unresolvable dependency never starts.

  **`envelope.DependsEntry.SplitHandle()`.** Helper that splits
  `Handle` into `(namespace, id)`. Pre-condition: every
  `DependsEntry` came from `envelope.Validate`, which already
  enforced the format via `dependsHandlePattern`. No error return
  — schema is the authoritative gate, runtime parsing trusts it.

  **`InstantiateVerified` flow.** Two new steps slot into the
  existing pipeline:
  1. After `envelope.Verify`, before any forge work: if a
     registry is wired and `manifest.Depends` is non-empty,
     `resolveDepends` validates every entry. Failure aborts
     immediately — no FS / events / tools handles are forged,
     no `Instance` is created, the guest never sees anything.
  2. After successful `ForgeInstance`, before
     `VerifiedInstantiation` returns: if a registry is wired AND
     the manifest's kind is `KindCapabilityProvider` AND
     `manifest.Provider.Exports` is non-empty,
     `RegisterProviderExports` writes one entry per export.
     Runs AFTER the forge so a forge failure doesn't pollute the
     registry; runs BEFORE return so a downstream consumer
     loaded immediately after sees the exports. Stage 5 will
     add an uninstall path that removes registry entries
     before re-registration; Stage 3 surfaces a duplicate
     re-install (same key, same exports) as an instantiation
     failure so the operator notices.

  **Daemon wiring.** `cmd/alf-daemon/wasm.go::setupWASMLoader`
  passes the registry to `runtime.NewInstantiator(...,
  runtime.WithHandleRegistry(handleRegistry))` alongside the
  existing `WithEventsBus` / `WithCrossFlowRegistry` options.
  `SeedHandleRegistry` stays as the explicit boot-seed call —
  the option only stores the registry; seeding is a separate
  step so a duplicate-seed wiring bug surfaces loudly at boot
  rather than racing during option processing.

  **Tests.** 11 new runtime tests in `depends_test.go`:
  - `DependsResolvedAgainstRegistry` — happy path with `alf:fs`
  - `DependsUnregisteredFails` — load-bearing acceptance criterion
    #1 of #392, returns `ErrDependsHandleNotRegistered`
  - `DependsAlfCoreHandleResolves` — Stage 2/3 schema-and-registry
    agreement pin (every documented core id resolves in a
    seeded registry)
  - `NoRegistryNoCheck` — preserves the legacy "no registry, no
    authority" path
  - `CapabilityProviderRegistersExports` — provider install
    populates the registry under SignerID.HexLower()
  - `ProviderThenConsumer` — full Stage 3 flow with shared trust
    store, consumer's `[[depends]]` resolves on the
    provider's just-registered exports (acceptance criterion #2
    of #392)
  - `TwoProvidersSameID` — acceptance criterion #3 of #392, two
    providers signed by different keys both claim the same
    handle id, distinguished by fingerprint namespace
  - `LLMProviderDoesNotRegister` — kind discriminates: only
    `capability-provider` mutates the registry, not
    `llm-provider`
  - `RegisterProviderExports_EmptyIsNoop` — nil + empty slices
    are no-ops, registry untouched
  - `RegisterProviderExports_FingerprintNamespace` — namespace
    matches `SignerID.HexLower()` exactly, no uppercase
    characters (manifest-syntax compatibility)
  - `DuplicateProviderInstallFails` — same bundle re-signed with
    a different key succeeds (different fingerprint = different
    namespace); same-key re-install would fail with the
    duplicate-rejection error (Stage 5 will add proper
    uninstall pathway)

  New helper `signBundleWithStore(t, manifest, bundle, store)` adds
  a fresh signer to a pre-existing trust store rather than creating
  one — used to build the "load provider, then load consumer trusted
  by the same daemon" scenario.

  **Stages 4–5 still pending**: `CheckImports` raw-imports
  pass-through + scope schema validation (M8 audit finding —
  Runtime-side validation against the provider's exported schema,
  not the provider's implementation) + transitive trust display
  (Stage 4); `alf provider list/install/remove` CLI + revocation
  cascade hook into #396 deliverable 3 + example shipped provider
  end-to-end (Stage 5).

- **#392 Stage 2 — runtime `HandleRegistry` + core registration**.
  Stage 1 shipped the manifest schema for capability providers. Stage 2
  ships the runtime registry the schema validation already implies: a
  daemon-wide table of every handle kind the loader can resolve at
  forge time. At boot, the registry holds the `alf:` namespace seeded
  from `AlfCoreHandleIDs`; Stage 3 will append installed providers'
  `[[provider.exports]]` under each publisher's fingerprint short.

  **`internal/capability/handle/registry.go`.** New types:
  `HandleKind` (Namespace + ID; `FullName()` returns `"<ns>:<id>"` —
  the manifest-syntax form callers compare against
  `[[depends]].handle`); `*HandleRegistry` (concurrent-safe via
  `sync.RWMutex`, many readers run alongside one writer);
  `AlfNamespace = "alf"` (the reserved namespace constant);
  `AlfCoreHandleIDs` (the closed allowlist of core kinds —
  `fs / http / exec / secrets / events.pub / events.sub / tool` —
  kept verbatim aligned with `envelope.coreHandleIDs`).

  **API surface.** Token-gated mutators: `Register(tok, k)` rejects
  any of (unminted token, empty namespace, empty id, `alf:`
  namespace with non-core id, duplicate `<ns>:<id>` already present)
  with a sentinel error; `RegisterCore(tok)` is a convenience that
  iterates `AlfCoreHandleIDs` so the daemon's boot path issues a
  single call to seed the entire alf: namespace. Read-only:
  `Lookup(ns, id) (HandleKind, bool)`, `List() []HandleKind`
  (sorted by FullName for deterministic output, returned as a fresh
  copy so callers cannot mutate registry state), `Len() int` for
  boot diagnostics. The token check uses
  `crypto/subtle.ConstantTimeCompare` matching `ForgeInstance`'s
  existing pattern; both gates draw from the same one-shot
  `mintedToken`.

  **Token never escapes Instantiator.** The runtime token is the
  one-shot authority that gates both `ForgeInstance` (handle
  forging) and `Register` (registry mutation). To drive the registry
  from outside the handle package without exposing the token, a new
  method `(*Instantiator).SeedHandleRegistry(*HandleRegistry) error`
  is the only sanctioned path — it wraps `RegisterCore` using the
  Instantiator's internal token. Stage 3 will add a sibling method
  for provider-installed exports (the providers manager will go
  through that path).

  **Daemon wiring.** `cmd/alf-daemon/wasm.go::setupWASMLoader` now
  constructs the registry via `handle.NewHandleRegistry()` after
  building the Instantiator, seeds it via `inst.SeedHandleRegistry`,
  and stores it on `wasmRuntime.HandleRegistry` so Stage 3's forge
  integration can consume it without a second mutation channel. Boot
  surfaces `[wasm-loader] handle registry seeded: 7 core kinds
  (alf:*)` so an operator can see at a glance whether the registry
  came up correctly.

  **Tests.** 15 new tests in `registry_test.go`:
  `RegisterAndLookup`, `LookupMiss`, `TokenGateRejectsZeroToken`
  (the load-bearing pin: a composite-literal `RuntimeToken` cannot
  forge authority because the `key` field is unexported),
  `TokenGateRejectsBeforeMint`, `EmptyNamespaceRejected`,
  `EmptyIDRejected`, `DuplicateRejected`,
  `AlfReservedToCoreKinds` (a provider attempting `alf:bluetooth.scan`
  is the load-bearing #392 invariant — alf: is the daemon's, only
  `AlfCoreHandleIDs` may register there even with the runtime token),
  `AlfAcceptsAllCoreIDs` (every documented core id passes),
  `RegisterCoreSeedsAllAlfKinds`, `RegisterCoreTwiceFails` (idempotence
  check — second call fails loudly so the boot wiring can't seed
  twice), `ListSortedByFullName`, `ListReturnsCopy` (caller mutation
  cannot affect registry state), `ConcurrentReadersWriter`
  (race-tested via `go test -race`), and
  `AlfCoreIDsCoverEveryDocumentedKind` (drift pin between
  `AlfCoreHandleIDs` and the §3.4 documented set — bug class:
  manifest passes envelope validation, then registry Lookup fails
  at runtime). Total handle-package suite green; race detector
  green.

  **Archtests.** 2 new in `internal/archtest/handle_registry_scope_test.go`:
  `TestNewHandleRegistryImportScopePinned` (only
  `internal/capability/handle`, `internal/runtime`, `cmd/alf-daemon`,
  `internal/archtest` may import `handle.NewHandleRegistry`) and
  `TestRegisterCoreCallerScopePinned` (only those packages may call
  `.RegisterCore(`). Belt-and-braces alongside the runtime-token
  check at the call site: even a future refactor that accidentally
  exposed the token couldn't widen the registry-mutation surface
  without also updating the allowlist — and that requires a
  reviewer sign-off in the same diff. Without this archtest, the
  only check would be the runtime token; with it, both
  compile-time importers AND runtime authority gate the boundary.

  **Stages 3–5 still pending**: `internal/runtime/providers/manager.go`
  provider lifecycle (discovery, verify, install) + `forgeGrants`
  extension to resolve `[[depends]]` via the registry (Stage 3);
  `CheckImports` raw-imports pass-through + scope schema validation
  + transitive trust display (Stage 4); `alf provider list/install/
  remove` CLI + revocation cascade + example shipped provider
  end-to-end (Stage 5).

- **#392 Stage 1 — manifest schema scaffolding for capability providers**.
  The user-extensible handle registry (#392) needs three pieces in the
  envelope schema before any runtime wiring can land: a way for a
  bundle to declare *what* handle kinds it exports, a way for a
  consumer to reference an exported kind, and a controlled escape
  hatch for raw WASI access. Stage 1 ships all three at the parse
  layer; Stages 2–5 wire the registry, forge integration, raw-imports
  pass-through, scope schema validation, and the operator CLI.

  **Kind enum split.** The legacy `kind = "provider"` value covered
  LLM-backend bundles only (claude / openai / ollama). #392's
  capability providers (Tier 2: Bluetooth, GPU compute, custom IoT —
  bundles that SIGN new handle types into the runtime registry) are a
  different concept: different sign-time ceiling, different runtime
  wiring, different install-UX prompt. Conflating them under one kind
  would force runtime-content inspection of the `[provider]` block
  to disambiguate — the exact "schema-tells-you-what-it-means-via-content"
  anti-pattern §3.3 of MANIFEST-SCHEMA forbids. Solution: rename the
  LLM-backend kind to `llm-provider` and add `capability-provider`.
  Manifests carrying the bare `kind = "provider"` are rejected with
  `ErrKindUnknown` so authors are forced to pick explicitly. No
  production manifest used the legacy value — the rename is a clean
  break, no migration burden.

  **`[provider]` block** — `[[provider.exports]].id` declares one
  handle kind name (lowercase / digits / dot / hyphen — e.g.
  `bluetooth.scan`, `gpu.compute`). Only valid when `kind =
  "capability-provider"`; declaring exports on any other kind fails
  with `ErrProviderBlockNotAllowedHere`. Empty `[provider]` block
  (no exports) is allowed on any kind so a degenerate scaffold-stage
  manifest doesn't break. Duplicate ids in a single block are a
  parse error. Stage 1 ships `id` only; the per-export scope schema
  (`schema_ref`) lands in Stage 4 alongside Runtime-side scope
  validation (M8 audit finding — scope checks happen Runtime-side,
  not in the provider).

  **`[[depends]]` block** — `handle = "<ns>:<id>"` is the namespace-
  scoped reference to a registry-resident handle kind. `<ns>` is
  either the reserved `alf` (for daemon-shipped core kinds) or a
  publisher-fingerprint short. The `alf:` namespace exposes a closed
  allowlist of `fs / http / exec / secrets / events.pub / events.sub
  / tool`; `[[depends]].handle = "alf:<other>"` is rejected with
  `ErrDependsHandleNamespaceReserved` so a manifest can't claim a
  core kind by collision (the only path to `alf:fs` is via the
  daemon's bundled forge code, never via a `[[provider.exports]]`
  declaration). Non-`alf:` namespaces pass format validation in
  Stage 1; Stage 3 (forge integration) will look up the concrete
  provider in the runtime registry and fail closed if it's not
  installed. `scope = {...}` is opaque at Stage 1 — any TOML table
  copies through unchanged. Duplicate `handle` values in one
  manifest are a parse error.

  **`[[raw_imports]]` block** — escape-hatch for guests that need a
  WASI function not exposed via a scoped handle (low-resolution
  clock for animation timing, exit code, etc.). Each entry carries
  `module` (`wasi:<package>/<interface>`), `function`, and
  `justification` (operator-facing string surfaced at install).
  Empty / whitespace-only justifications are rejected — operators
  must see a real explanation. The classifier is **default-deny**:
  - **Forbidden prefixes** (must use a scoped handle instead;
    `ErrRawImportForbidden`): `wasi:filesystem/` (use `[fs]` /
    `alf:fs`), `wasi:sockets/` (use a network provider),
    `wasi:random/random` (future `alf:crypto`), `wasi:io/streams`
    (scoped fd handle).
  - **Allowed prefixes** (still surface a warning at install in
    Stage 5): `wasi:clocks/monotonic-clock`, `wasi:clocks/wall-clock`
    (daemon clamps resolution to defeat timing channels),
    `wasi:cli/environment` (explicitly scoped env vars per
    manifest), `wasi:cli/{exit,stdin,stdout,stderr,terminal-input,
    terminal-output}` (pure compute / terminal — no host fs reach).
  - **Anything else**: `ErrRawImportNotInAllowlist`. Adding a new
    allowed entry is a deliberate schema change requiring an
    update to MANIFEST-SCHEMA.md §3.4 + the
    `internal/archtest/raw_imports_classification_test.go` pin
    alongside the slice. Forbidden takes priority over allowed —
    if a future allowed prefix incidentally subsumes a forbidden
    one, the classifier returns forbidden.

  Stage 1 validates the manifest at parse time. Stage 4 will wire
  the allowed imports through `internal/runtime/wasm/CheckImports`
  so the guest can link the symbols; until then a guest that
  imports an allowed-but-not-yet-wired symbol fails at runtime
  instantiation with `ErrLyingManifest`.

  **Tests.** 24 new envelope tests covering each new validation
  rule + the kind split + the namespace allowlist (12 happy paths
  + 12 error sentinels). New archtest
  `TestRawImportsClassificationPinned` (in `internal/archtest/`)
  freezes the forbidden + allowed + core-handle-id sets verbatim
  against the spec — drift in either direction (entry removal or
  re-wording) fires CI. Source-of-truth pin reads the schema source
  directly rather than importing the unexported slices, so a
  contributor can't "fix" a failing archtest by mutating the slice
  to match what the test expects.

  **Doc updates.** MANIFEST-SCHEMA.md §3.3 (kind enum updated +
  legacy-rename rationale), §3.4 (three new sub-sections detailing
  `[provider]`, `[[depends]]`, `[[raw_imports]]` with field tables
  + reserved namespace allowlist + classifier sets), §4.4 (split
  into `4.4 capability-provider` example + new `4.5 llm-provider`,
  marketplace-app renumbered to 4.6). **Stages 2–5 still pending**:
  `HandleRegistry` interface + core registration (Stage 2);
  `internal/runtime/providers/manager.go` discovery + forge
  integration (Stage 3); `CheckImports` raw-imports pass-through +
  scope schema validation + transitive trust display (Stage 4);
  `alf provider list/install/remove` CLI + revocation cascade +
  example shipped provider end-to-end (Stage 5).

- **#395 Stage 2 chunk 4 — `SecretValue` redaction + vault
  user-scope partition**. Closes the secrets-flow isolation gap
  per ticket §3: even within capability-scope, secret values must
  not leak into LLM context via the *composition* surface (a benign
  `%v`, `json.Marshal` in a log line, memory-recall snapshot
  persisting the plaintext). `SecretsHandle.Get(ctx, name)` now
  returns a `handle.SecretValue` instead of a raw string. The
  type:
  - **Redacts via every standard formatter.** `String()` /
    `GoString()` return `<redacted>` (covers `fmt.Sprintf %v / %s /
    %q / %#v / %+v` — every formatter that routes through
    `Stringer`/`GoStringer`). `MarshalJSON` returns `"<redacted>"`.
    `MarshalText` returns `<redacted>`. `MarshalBinary` returns
    `ErrSecretValueNotMarshalable` so gob / msgpack / any binary
    serialiser fails LOUDLY rather than silently emitting bytes.
  - **Provides two trusted-caller paths.** `Reveal() string` is
    audit-greppable — every call site is meant to be visible to a
    security review. `ConsumeInto(io.Writer)` writes the plaintext
    and zeroes the internal buffer in place; subsequent calls are
    no-ops returning `(0, nil)`. Use for HTTP header injection,
    HMAC seeds, anywhere the secret's lifetime can be bounded to a
    single write.
  - **Holds bytes, not a string.** Internal buffer is `[]byte` so
    `ConsumeInto` can scrub in place; strings are immutable in Go
    and cannot be zeroed without an extra copy.
  - Constructors: `NewSecretValue(b []byte)` borrows the caller's
    buffer (so a vault reader scrub-on-error also clears the
    SecretValue); `NewSecretValueFromString(s)` copies, allocates
    a fresh slice.
  Vault user-scope partition: structurally in place via
  `internal/admin/userkey/` (already pinned by
  `TestAdminPackageBoundary` to admin-only consumers).
  `internal/sandbox/secrets/Manager` does not expose paths under
  `<dataDir>/keys/` or `<dataDir>/admin/`, so no `secrets.Handle`
  constructor can ever target user-scope material — the structural
  partition the chunk-4 spec asked for is already enforced.
  14 new tests in `internal/capability/handle/secret_value_test.go`:
  StringRedacts, GoStringRedacts (`%#v` is the most common debug
  leak surface), QuoteVerbAlsoRedacts (`%q` falls back to Stringer
  for non-string types), MarshalJSONRedacts (decoded through
  `json.Unmarshal` to verify semantic value, not the byte form),
  StructMarshalingRedacts (the common case: SecretValue field in
  a tool-output struct), MarshalBinaryRefuses, MarshalTextRedacts,
  RevealReturnsPlaintext, RevealOnZeroValue,
  ConsumeIntoWritesAndScrubs, ConsumeIntoNilReceiverNoOp,
  ConsumeIntoTwiceIsIdempotent, FmtPrintEverywhereStaysRedacted
  (sweep across every fmt verb), NewSecretValueBorrowsBuffer (the
  vault scrub-on-error contract), NewSecretValueFromStringCopies.
  **Stage 2 complete.** Reflection-based `Runtime.Invoke` output
  sanitizer (chunk 4 ticket §3 third bullet) deferred to `#411`
  — same dependency as `#398`'s output-sanitization piece;
  redaction at the type level is the load-bearing piece, the
  sanitizer is defence in depth.

- **3-layer sandbox E2E harness — beta soak gates for #399 + #400**.
  New `internal/runtime/sandbox_3layer_test.go` carries three
  integration tests run via `go test -run TestSandbox_`. Each one
  is the "the layered sandbox holds" claim executed against the
  production forge / injector path — not a unit test of one
  helper. Gates the `release/0.8.0` beta soak; a regression in any
  of the wiring touched by #391 / #399 / #400 / #386 / #389 surfaces
  here.
  - `TestSandbox_L33_EventCrossFlow_PrivateByDefault` — 4-cap
    integration through `Instantiator` + `events.Bus` + cross-flow
    registry. Cap-A exports `chat.log`; cap-B subscribes (gets
    EventSub); cap-C declared nothing (no EventSubs); cap-D
    subscribes to an unexported topic (forge skips silently).
    Round-trip publish: cap-B receives, `bus.SubscriberCount`
    confirms cap-B is the sole receiver. Pins the §3.3 acceptance
    criterion lifted verbatim from the #399 issue body.
  - `TestSandbox_L33_BusRefusesUndeclaredSubscriber` — second-line
    invariant at the bus layer (defence in depth behind the
    forge): a Subscribe on a (publisher, topic) the publisher
    never exported gets no events when the publisher publishes on
    a different topic.
  - `TestSandbox_L32_KernelPromptHolds_AgainstFetchedContent` —
    drives `WrapFetchedContent` with hostile content (literal
    `</fetched_content>` bytes attempting marker breakout) through
    `KernelPromptInjector` → `capturingProvider`. Asserts kernel
    prompt at position 0, opening + closing tag both nonce-bound
    (SEC-002), attacker's bare close bytes appear INSIDE the
    marker as content, kernel prompt mentions each marker tag
    name so the agent has an explicit rule to demote inner
    contents. Pins the #400 acceptance criterion's structural
    premise; the LLM-behavioural side ("model actually refuses")
    is a soak-window observability item rather than a unit gate.

- **#389 Stage 2 — active-skill boundary narrows LLM tool surface**.
  Stage 1 shipped the structural core (schema, forge, loader). Stage 2
  wires the orchestrator path so the §3.1 promise actually holds at
  the LLM tool spec layer: a manifest-shipped skill's
  `[[tools.declares]]` block now bounds what the model sees per
  turn. New `skills.NarrowToolsByDeclares(lookup, activeSkills,
  tierTools)` returns the intersection of "tier-allowed" and "any
  active skill's declares" preserving tier order; YAML-only active
  skills (no manifest yet) return nil from the lookup → tier
  passthrough (transition compromise). New helper
  `skills.DeclaresFromVerified` flattens a `*VerifiedSkill`'s
  manifest into a `[]string` for daemon wiring. `pipeline.ChatEngine`
  gains `SkillDeclaresLookup` field + `SetSkillDeclaresLookup`
  setter (same wire-after-construction pattern as `SetRuntime` —
  `skillsRuntime` is built late). `processStandard` hoists
  `activeSkills` out of the prompt-injection branch and applies the
  narrow before the API tool loop is wrapped AND before
  `provider.Params.Tools` is populated; same narrow on the fallback
  path. Soak diagnostics: a `[comms] active-skill boundary narrowed
  tools X → Y` log line surfaces every actual narrow.
  `cmd/alf-daemon/skillsRuntime.DeclaresLookup(name)` walks
  `s.verified` linearly (the list is small — 5 shipped + a handful
  of operator skills); `Replace`'s slice swap is reflected
  immediately by future calls — no cache to invalidate. Boot wires
  `commEngine.SetSkillDeclaresLookup(skillsRt.DeclaresLookup)` right
  after `SetEngine`. 17 new tests across two packages: 12 in
  `internal/skills/narrow_test.go` covering the load-bearing
  intersection-narrowing invariant + 9 edge cases (nil lookup, empty
  active skills, empty tier tools, tier order preservation, union
  across multiple skills, YAML-only mix passthrough, declares
  outside tier ignored — declares cannot extend the tier ceiling,
  zero overlap → empty, mixed YAML + manifest skills); 5 in
  `cmd/alf-daemon/skills_loader_test.go` covering nil receiver,
  empty verified, hit-by-name, malformed-entry skipping,
  verified-slice mutation visibility. **Still deferred**: legacy
  `MirrorInto + skillCapability` deletion (independent demolition
  PR once every shipped + user skill ships a manifest.toml).

- **#395 Stage 2 chunk 3 — `alf pending` + `alf ratify` + persistent
  `pending.DirStore`**. Stage 1 shipped an in-memory `pending.Store`
  contract; the production daemon and CLI now have an on-disk
  implementation that survives `alf restart`. New `*DirStore` at
  `<dataDir>/admin/pending/<id>.json` (mode 0o600, parent 0o700) —
  one file per `Item`, atomic tmp+rename per `Append`, unlink per
  `Approve`/`Deny`. ID allocation: scanned at construction time;
  next id = max existing + 1 (zero-padded decimal so lex-sort matches
  numeric). Refuses construction if `<dir>` has 0o077 perms set.
  Operator-facing CLI:
  - `alf pending [list]` — read-only enumeration, no TTY required.
    Five-column table (ID / KIND / AGE / FROM / PAYLOAD), oldest-
    first. Empty queue prints a friendly message and the queue dir.
  - `alf ratify <id> [--deny]` — approve (default) or deny a single
    item. Refuses non-TTY stdin. Shows the item's kind / from /
    created-at / full payload before prompting `Type 'yes' to
    approve|deny:`. Argument-order independent. Approving prints
    a note that queue removal does NOT itself execute the requested
    operation — the consumer that `Append`'d the item is responsible
    for the actual effect.
  Wiring: `cmd/alf/admin/Env` gains `PendingDir` + `DefaultPendingDir()`;
  `runAdmin` dispatches the two new commands. No daemon-side
  wiring yet — there is no consumer that `Append`'s items at boot;
  the Runtime → Append plumbing lands when an LLM-built widening
  capability reaches that point. New helpers in `pending`:
  `NewDirStore(dir, now)`, `DirStore.Dir()`, `DefaultDir(dataDir)`.
  10 new DirStore tests + 15 new CLI tests = 25 across two packages,
  including persistence-across-restart, path-traversal payload
  rejection (`../../etc/passwd` cannot escape `<dir>`), 50-way
  concurrency, full item-details rendered before the confirm prompt.
  **CC `/admin/ratify/*` route deferred** — needs a separate browser-
  session trust domain in the CC HTTP surface + Svelte UI; tracked
  as a chunk-3.5 / CC follow-up. The CLI surface is sufficient for
  the beta soak. **Stage 2 chunk 4 still pending**: vault user-scope
  partition + `SecretValue` redaction.

- **#395 Stage 2 chunk 2 — `alf keygen` + `alf sign` (Tier-3
  user-endorsed signing)**. Tier-2 daemon key auto-signs only what
  the §7.3 ceiling allows (no cross-flow events; SEC-004 enforces
  this). Anything that widens authority beyond that ceiling now has
  a TTY-only CLI path: the operator mints a user-endorsed key with
  `alf keygen`, then signs the bundle with `alf sign`. New package
  `internal/admin/userkey/` persists the key under
  `<dataDir>/keys/user-endorsed.json` (mode 0o600, parent 0o700,
  atomic tmp+rename) encrypted with ChaCha20-Poly1305 under a
  32-byte argon2id-derived key (t=3, m=64MiB, p=4); salt + nonce
  are 32 / 12 random bytes per seal. AEAD AAD binds schema version,
  KDF id, KeyID and the public key — any field swap between records
  surfaces as `ErrPassphrase`, indistinguishable from a typo from an
  offline attacker's perspective.
  - `alf keygen [--export-pub <path>] [--comment "..."] [--force]`
    — refuses non-TTY stdin, prompts twice for a passphrase
    (≥12 bytes), persists the encrypted record, prints fingerprint +
    storage path. Re-running without `--force` on an existing record
    fails loudly; `--force` requires explicit "yes" confirm and warns
    that bundles signed with the old key will fail verification.
    `--export-pub` writes a minisign-format `.pub` for distribution
    via `alf trust add` on other machines.
  - `alf sign <bundle-dir> [--bundle <path>] [--at <RFC3339>]`
    — refuses non-TTY stdin, reads `manifest.toml`, validates the
    schema (NO Tier-2 ceiling check — Tier 3 IS the path that may
    widen authority beyond the daemon key's ceiling), canonicalises,
    prompts for the passphrase, signs, writes `manifest.sig`
    atomically. Bundle-artefact detection follows manifest.kind:
    `wasm-tool`/`wasm-app` → single `*.wasm` in the bundle dir;
    `marketplace-app` → `bundle.zip`; `skill`/`provider` → no
    artefact (BundleHash empty in trusted comment, accepted by
    `envelope.Verify`). `--bundle` overrides detection;
    `--at <RFC3339>` overrides signed-at.
  Wiring: new `cmd/alf/admin/env.go` carries a shared `Env` (the
  legacy `TrustEnv` is now a type alias). `cmd/alf/main.go` adds a
  single `runAdmin(handler, args)` factory that builds the
  production env once (real `os.Std*`, `golang.org/x/term`-backed
  terminal check + no-echo passphrase reader, real `time.Now`,
  install-layout-resolved paths) and dispatches across `trust`,
  `keygen`, `sign`. New dependency `golang.org/x/term` v0.42.0 (the
  canonical no-echo passphrase primitive). New userkey API
  `WithPrivateKey(pass, fn)` zeroes the plaintext private key on
  return; the CLI uses it for the raw Ed25519 global-comment sig
  that minisign expects (BLAKE2b-prehashed `Sign` is the wrong
  primitive there). Archtest fix in `topLevelConsumer`: was silently
  remapping `cmd/alf/admin` → `internal/cmd` and missing the
  allowlist; now strips `internal/`, `cmd/`, or `pkg/` correctly.
  11 new tests in `cmd/alf/admin/keysign_test.go` (round-trip with
  and without WASM artefact, non-TTY refusal, no-key path,
  passphrase mismatch, short passphrase, ambiguous-bundle, missing
  manifest, force-overwrite, export-pub) + 1 new userkey API method.
  Round-trip verification uses `envelope.ParseSignatureFile` +
  `Canonicalize` + `VerifySignature` + `VerifyGlobalComment` directly
  — same primitives `envelope.Verify` chains internally; the
  high-level call site stays reserved for runtime per #388
  deliverable 2 (`TestOneVerifyCallSite`). **Stage 2 chunks 3–4
  still pending**: persistent `pending.Queue` + `alf pending` +
  `alf ratify` + CC `/admin/ratify/*` route in a separate trust
  domain; vault user-scope partition + `SecretValue` redaction.

- **#395 Stage 2 chunk 1 — operator-managed trust store + `alf trust`
  CLI**. The daemon's WASM trust store is now `*envelope.DirTrustStore`
  bound to `<dataDir>/trust/` instead of an in-memory store seeded only
  with the auto-bootstrapped daemon key. Operators add, remove, and
  revoke third-party signing keys without daemon roundtrip via four new
  TTY-only subcommands of `alf`:
  - `alf trust list` — prints fingerprint + status (`trusted` /
    `revoked@<RFC3339>`) + untrusted-comment for every key under
    `<dataDir>/trust/`. The daemon-bootstrapped key lives in
    `<dataDir>/keys/daemon.json` (auto-trusted at boot) and is
    intentionally not surfaced — operators are not meant to manage it.
  - `alf trust add <pub-file> [--comment "..."]` — parses a minisign
    `.pub` file, prompts for explicit "yes" on the TTY, then atomically
    persists `<keyid>.pub` (tmp + rename, mode 0o644). Re-Adding an
    existing fingerprint clears any prior `.revoked` sidecar (matches
    `MemoryTrustStore.Add` semantics).
  - `alf trust remove <fingerprint>` — deletes `<keyid>.pub` and any
    companion `<keyid>.revoked`. Idempotent on disk; rejects unknown
    fingerprints loudly so a typo doesn't silently no-op.
  - `alf trust revoke <fingerprint> [--at <RFC3339>]` — writes
    `<keyid>.revoked` with an RFC3339 timestamp; the pubkey file stays
    in place. Default `--at` is now (operator's TTY clock); `--at`
    overrides for "compromise actually started earlier than this
    command" cases. `envelope.Verify` rejects bundles whose
    `signed-at >= revoked-at` via the existing `Revoker` interface.
  All mutating commands refuse on non-TTY stdin (`ErrNonInteractive`)
  — non-TTY input is the canonical prompt-injection signature this
  boundary exists to block. Changes take effect on the next `alf
  restart` (SIGHUP-driven hot-reload deferred to a follow-up). New
  package `cmd/alf/admin/` is pinned by two archtests:
  `TestAdminCLIPackageBoundary` (no consumer outside `cmd/alf/*` may
  import it) and `TestAdminCLIDoesNotImportRuntime` (admin CLI must
  not pull in `internal/runtime`, `internal/tooling`,
  `internal/capability/handle`). New persistence API on the envelope
  side: `DirTrustStore.Persist(pub, comment)`,
  `DirTrustStore.PersistRemove(id)`,
  `DirTrustStore.PersistRevoke(id, t)`, plus a `.revoked` sidecar
  loader so operator-set timestamps survive restarts (CRL-set
  timestamps remain memory-only by design — the upstream `Refresher`
  repopulates them on the next tick). New `envelope.ErrKeyNotInStore`
  sentinel. 8 new admin-CLI tests, 7 new envelope tests, 2 new
  archtests. **Stage 2 chunks 2–4 still pending**: `alf keygen` +
  `alf sign` (user-endorsed key + passphrase), `alf pending` +
  `alf ratify` (persistent ratification queue + CC trust domain),
  vault user-scope + `SecretValue` redaction.

### Security

- **SEC-004 — auto-sign §7.3 Tier-2 ceiling enforcement**.
  Both auto-signers (`wasm.Loader.autoSign` for WASM bundles and
  `skills.daemonAutoSigner.Sign` for skill bundles) signed any
  schema-valid manifest the local daemon key could produce a
  signature for. This silently widened the daemon key's authority
  beyond the §7.3 Tier-2 ceiling: an LLM-built or sideloaded bundle
  declaring `[[events.subscribes]]` against another cap would be
  auto-signed and cross-flow-forged at next boot, with no operator
  intervention. The user-endorsed key (Tier 3) is the only level
  authorised to widen authority across capability boundaries. Fix:
  new `envelope.EnforceTier2Ceiling(*Manifest) error` returning
  `ErrCeilingExceeded` when a manifest declares cross-flow event
  subscriptions (the only widening surface today; future widening
  blocks slot into the same function). Both auto-signers call
  `Validate` then `EnforceTier2Ceiling` before signing — refusing
  early so `manifest.sig` is never persisted for a ceiling-violating
  bundle. Operator recourse is `alf keygen` + `alf sign --key
  user-endorsed`, then re-load. 7 new tests:
  `TestEnforceTier2Ceiling_*` (6 cases pinning bare/own-dir-fs/own-
  topic-export/cross-flow-rejected/tools-declares-accepted/nil-
  rejected) and integration test
  `TestLoader_AutoSign_RefusesCrossFlowSubscription` (subscriber
  bundle blocked, `manifest.sig` not written, error mentions
  Tier-2 ceiling). Pre-existing cross-flow loader tests migrated
  to a `preSignSubscriberBundle` helper that mints a manual
  signature with the trust-store key — simulating the
  user-endorsed flow these tests pre-date.
- **SEC-006 — FSHandle symlink TOCTOU + mode tightening**.
  `FSHandle.Read` / `Write` used `os.ReadFile` / `WriteFile` which
  silently follow symlinks. A symlink installed inside the
  capability's scope (by a co-tenant cap, the operator, or via an
  earlier in-scope write) could redirect reads to leak files outside
  scope, or redirect writes to clobber arbitrary paths. Mode `0o644`
  on writes also left files world-readable inside shared-volume
  container deployments. Fix: open with `O_NOFOLLOW` plus an `Lstat`
  pre-check (defends against platform-specific O_NOFOLLOW drift) so
  a symlink at the leaf surfaces as new `ErrSymlinkRefused` rather
  than transparently dereferencing. Write mode tightened to `0o600`
  (owner read/write only). New `readFileNoFollow`,
  `writeFileNoFollow`, `isSymlinkErr` helpers in `handle/fs.go`.
  3 new tests: `TestFSHandle_RefusesSymlinkRead` (in-scope symlink
  to outside target → bytes do NOT leak),
  `TestFSHandle_RefusesSymlinkWrite` (symlink to outside file → file
  NOT clobbered), `TestFSHandle_WriteUses0o600`.
- **SEC-007 — HTTPS-only CRL URL (boot-time validation)**.
  `ALF_CRL_URL` accepted plaintext HTTP for any host. Combined with
  SEC-001 replay this widened the rollback surface — a net-position
  attacker could swap CRL bytes without touching TLS. Fix: new
  `crl.ValidateCRLURL` enforces `scheme = https` for non-loopback
  hosts; `127.0.0.0/8` / `::1` / `localhost` over HTTP stay allowed
  for local-dev and httptest harnesses. `HTTPSource.Fetch`
  re-enforces at call time so a programmatic misuse cannot bypass
  the daemon-boot check; `cmd/alf-daemon/setupCRL` fails fast on
  misconfigured `ALF_CRL_URL` rather than waiting for the first
  Tick. New `ErrInsecureURL` sentinel. 5 new tests:
  `TestValidateCRLURL_AllowsHTTPS`,
  `TestValidateCRLURL_RejectsPlaintextPublicHost`,
  `TestValidateCRLURL_AllowsLoopbackHTTP`,
  `TestValidateCRLURL_RejectsUnknownScheme` (file/ftp/javascript/
  gopher all refused), `TestHTTPSource_RejectsPlaintextPublicURL`.
- **SEC-002 — marker breakout via per-turn nonce framing**. The
  `<tool_output>` / `<capability_content>` / `<fetched_content>`
  markers that the §3.2 kernel prompt anchors on were structurally
  trivial: a tool that emitted the literal closing-tag bytes inside
  its own content broke out of the wrapper, forging a pseudo-system
  segment the kernel prompt's "NOT authoritative" rule no longer
  covered. Reachable from any LLM-controlled tool that can return
  attacker-chosen bytes (every WASM tool today). Fix: every wrap
  function now emits `<tag_{NONCE}>...</tag_{NONCE}>` with a
  literal `{NONCE}` placeholder; the `KernelPromptInjector` at
  every `Invoke` generates a fresh 16-hex random nonce
  (`crypto/rand`-backed), substitutes it across the kernel prompt
  + every caller `SystemPrompts` entry + the user prompt + every
  `ConvMessage.Content`, then propagates the nonce through
  `context.Context` so `ToolLoop`'s loop-local wraps for tool
  outputs in subsequent iterations get the same materialised
  nonce. A capability cannot guess the per-Invoke value, so
  closing-tag bytes inside its content stay literal data — the
  real closing tag carries the nonce. New primitives:
  `llm.NoncePlaceholder`, `llm.NewNonce`, `llm.SubstituteNonce`
  (with private mirrors `noncePlaceholder`, `newMarkerNonce`,
  `substituteMarkerNonce` in `internal/ai/provider` because the
  foundation rule forbids the cross-import). Tests:
  `TestKernelPromptInjector_SubstitutesNonceAcrossInputs`,
  `TestKernelPromptInjector_NoncesDifferAcrossInvokes` (50
  consecutive Invokes, no duplicate nonce),
  `TestKernelPromptInjector_NoncePropagatedViaContext`,
  `TestNewNonce_ProducesRandomDistinctValues` (1000 draws),
  `TestSubstituteNonce_ReplacesPlaceholderEverywhere`,
  `TestWrapToolOutput_BreakoutAttempt_IsContained` (hostile
  closing-tag bytes inside content stay contained).
- **SEC-005 — wire BuildScopedToolSpecs into the Chat tool surface**.
  The §3.1 active-skill tool-surface filter was implemented but never
  wired into production: the legacy `buildToolSpecs` helper (no
  allowlist) was the only producer, so every chat turn exposed every
  installed capability to the LLM regardless of which skill was
  active. Fix: `runtime.Chat` now routes through
  `BuildScopedToolSpecs`, gated by a new
  `ChatRequest.ActiveSkills []capability.ID` field. The legacy
  unfiltered helper was deleted; `BuildScopedToolSpecs` gained
  nil-allowlist semantics (= legacy "all manifests" surface) so
  callers without an active-skill boundary still work pending #389
  Stage 2 orchestrator wiring. Two new archtests:
  `TestNoLegacyBuildToolSpecsHelper` (the deleted symbol cannot
  re-appear in production code) and
  `TestBuildScopedToolSpecsIsWiredInChat` (the wire-in cannot be
  silently reverted to inline projection). Two new behavioural
  tests: `TestChat_ActiveSkillsNarrowsToolSurface` and
  `TestChat_NilActiveSkillsKeepsLegacySurface`.
- **SEC-001 — CRL anti-replay (monotonic IssuedAt high-water)**.
  Every signed CRL applied by the refresher updates an in-memory
  high-water mark — the IssuedAt of the most recently applied CRL.
  The mark is persisted in the cache meta (`crl.meta.json`'s new
  `last_crl_issued_at` field) and re-loaded on the first Tick after
  every daemon boot. CRLs whose `IssuedAt` is strictly older than
  the high-water are rejected as `ErrCRLReplay` on **both** the
  source path (MitM / compromised CDN replays an older valid CRL
  to roll back a revocation) and the cache path (attacker writes
  an older signed CRL into `<dataDir>/crl/crl.json` while leaving
  meta untouched). Equal `IssuedAt` is treated as idempotent
  (legitimate boot-from-cache scenario re-applies the same CRL).
  `Cache.Save` signature gained an `issuedAt time.Time` parameter;
  new `Cache.LoadIssuedAt()` method exposes the persisted
  high-water for cross-restart enforcement. Pinned by 4 new tests:
  `TestRefresher_SourceReplayRejected`,
  `TestRefresher_HighWaterPersistsAcrossRestart`,
  `TestRefresher_CacheReplayRejected`,
  `TestRefresher_EqualIssuedAtIdempotent`.
- **SEC-003 — events bus publish/cleanup race**. `Bus.Publish`
  snapshotted the route slice under `RLock`, released, then sent on
  the queue without coordination with `Subscribe`'s cleanup func —
  which acquired `Lock`, removed the route, and `close(q)`. A
  publisher that snapshotted before cleanup ran and sent after it
  closed the channel triggered `panic: send on closed channel`,
  killing the daemon process. Reachable from any LLM-driven
  publish/revoke loop. Fix: hold `RLock` across the fan-out so
  cleanup is strictly serialised after every in-flight Publish;
  sends are non-blocking (`select+default`) so the lock is held
  for ~µs even with thousands of subscribers. Pinned by
  `TestBus_PublishCleanupRace` (32 subscribers churning, 4
  publishers; panics within 100ms without the fix).
- **CVE patch — toolchain bumped to `go1.26.2`** (was `go1.26.1`
  via Dockerfile `golang:1.25`). `govulncheck` flagged 4 stdlib
  CVEs reaching production code: `GO-2026-4947` /
  `GO-2026-4946` (x509 chain-build / policy-validation DoS),
  `GO-2026-4870` (TLS 1.3 KeyUpdate DoS), `GO-2026-4866`
  (excludedSubtrees case-sensitivity auth bypass). All fixed in
  `1.26.2`. `go.mod` carries a `toolchain go1.26.2` directive;
  Dockerfile pinned to `golang:1.26.2-bookworm`. Post-bump
  `govulncheck`: zero vulnerabilities reaching code.

### Fixed

- **3-tier alignment audit (D1)** — `[[fs.writes]]` declarations in a
  signed envelope manifest were silently dropped at the forge layer:
  the legacy `permissionsFromEnvelope` shim routed only `[[fs.reads]]`
  into `PermissionSet.FilePaths`, so any tool declaring writes loaded
  fine but every `alf_fs_write` returned `ErrOutOfScope`.
  `Instantiator.InstantiateVerified` now forges `FSHandle` directly
  from the typed `envelope.Manifest.FS` with both `Reads` and `Writes`
  populated. Pinned by `TestInstantiateVerified_FSWritesRouted`.
- **3-tier alignment audit (D6)** — `<tool_output>` /
  `<capability_content>` markers from the §3.2 kernel prompt had no
  syntactic anchor in production: the wrap helpers in
  `internal/runtime/llm` existed but were not called outside their
  own tests. Plumbed at the three production sites that feed text
  into the LLM context: tool result in `internal/runtime/impl.go`
  (legacy chat loop), tool result in `internal/ai/provider/toolloop.go`
  (API tool loop, inlined to avoid foundation cross-import), and
  matched-skill body injection in `internal/runtime/agents/prepare.go`.
  Memory store keeps the unwrapped text so recall is not polluted by
  marker tags. Pinned by `TestChat_ToolResultMarkerDiscipline`,
  `TestWrapToolOutputForLLM_PinsMarkerShape`, and
  `TestPrepareOrchestration_SkillBodyWrappedWithMarker`.

### Added

- **#396 Stage 1 — revocation end-to-end (deliverables 1, 3, 4)**.
  Three commits land the load-bearing core of the §8 revocation
  story; CRL distribution + offline behaviour stay deferred to
  Stage 2.
  - **Deliverable 1 (timing acceptance)** — `Instance.Close()` was
    already wired through `lifecycleCtx` + `context.AfterFunc` in
    #391, but no test pinned the §8 timing budget. New
    `TestCloseTiming_*` (4 cases) prove HTTP / Exec / Tool in-flight
    ops unwind within 200ms after `Close`, and that 50 concurrent
    `Close()` calls don't deadlock — pre-condition for the
    `RevokeByKey` path that closes N Instances in parallel.
  - **Deliverable 3 (key-based revocation)** —
    `Instantiator.RevokeByKey(KeyID)` closes every live Instance
    forged from a bundle signed by the given fingerprint. The
    Instantiator now keeps a self-pruning live registry indexed
    by signer key; entries drop via a watcher goroutine when
    Close fires, regardless of whether Close came from the user,
    a RevokeByKey, or a future provider cascade. Audit log line
    per closed Instance via the configurable `WithRevocationLogger`
    option. `VerifiedInstantiation` now exposes `SignerID` +
    `SignedAt` so loaders can correlate without re-parsing the
    envelope. 6 tests including in-flight cancellation under
    budget and concurrent Close + RevokeByKey safety.
  - **Deliverable 4 (not-valid-after enforcement)** — new
    `envelope.Revoker` interface; `MemoryTrustStore.Revoke(KeyID,
    time.Time)` records a not-valid-after stamp;
    `envelope.Verify` rejects bundles whose `signed-at` is at or
    beyond it (strict-before semantics — boundary equality
    rejects). New `ErrSignerKeyRevoked` is distinct from
    `ErrSignerNotTrusted` so operators see the correct
    remediation. `Add()` clears any prior revocation (re-trust
    flow); `Remove()` clears alongside the key. 10 tests cover
    happy path, boundary, repeated revoke, re-trust, unknown
    key no-op, non-Revoker store fallback.
  - **Operator flow now functional**: `store.Revoke(fp, now)`
    blocks future loads; `inst.RevokeByKey(fp)` closes live ones.
    Deferred to Stage 2: signed CRL distribution, offline N-day
    fail-safe, clock-skew detection, `alf trust revoke` CLI
    (depends on #395 Stage 2 admin boundary).
- **#396 Stage 2 — CRL distribution + offline cache + clock-sanity
  (deliverables 5, 6, 7)**. Four commits close out the §7.7 + §8
  revocation pipeline end-to-end; D2 (provider cascade) + D8
  (`alf trust revoke` CLI) remain deferred to their respective
  blocking tickets (#392 / #395 Stage 2).
  - **Deliverable 5 (CRL primitive)** — new `internal/capability/envelope/crl.go`.
    `CRL` + `CRLEntry` types, embedded-signature wire format
    (`{"crl": {...}, "signature": "<base64>"}` — no sidecar). The
    signature covers `CanonicalCRLBytes(payload)` — same JCS rules
    as `Canonicalize` (alphabetical keys, NFC strings, RFC3339 UTC,
    no whitespace). `ParseSignedCRL` re-canonicalizes the parsed
    payload server-side before verifying so signer + verifier
    agree on byte layout without sidecar gymnastics, avoiding the
    parser-divergence attack class (§7.10.1).
    `MemoryTrustStore` gained a second revocation map (`crlRevokedAt`)
    so operator-set `Revoke()` and CRL-set `ApplyCRL()` are
    independent channels. `RevokedAfter` returns the strictest
    (earliest) of the two — neither can soften the other. `Add()`
    clears operator-set only (CRL is upstream-authoritative);
    `Remove()` clears both. Re-applying a CRL replaces (not
    merges) the previous CRL state. `KeyID` gained `MarshalJSON` /
    `UnmarshalJSON` (16-char uppercase hex) + `ParseKeyIDHex`
    helper. 15 tests.
  - **Deliverable 6 (offline cache + 30-day fail-safe)** — new
    `internal/capability/crl/` package layered above `envelope/`.
    `Source` interface with `HTTPSource` impl (4 MiB body cap +
    distinct `ErrSourceUnavailable` / `ErrSourceMalformed`
    sentinels), `Cache` interface with `FileCache` impl (atomic
    `crl.json` + `crl.meta.json` writes under `<dataDir>/crl/`,
    payload-size mismatch detection), and the `Refresher`
    orchestrator. Per-Tick algorithm: fetch from Source on
    success → save cache + apply; on Source unavailable → load
    cache → apply if valid; on Source malformed → reject (active
    mis-serve does NOT trigger cache fallback); compute
    `age = now - lastFetched` and log `[crl] OFFLINE FAIL-SAFE`
    when `age >= GracePeriod` (default `30 * 24h` per §7.7).
    Never aborts. `Run()` drives Tick on `Interval` (default 6h).
    13 tests.
  - **Deliverable 7 (clock-sanity)** — new
    `internal/capability/envelope/clocksanity.go`. `CheckBootClock`
    refuses if `now` is more than 1y before `BuildTime`
    (`ErrClockTooEarly`); one-sided — wildly future clocks accept,
    only the past is policed. `WallClockSkew` + `MonitorClockSkew`
    sample wall vs monotonic delta and warn at >6h drift.
    `SkewFromDeltas` exposes the pure math for tests (Go can't
    synthesize a `time.Time` with mismatched wall/monotonic).
    `BuildTime` is link-time-injected via
    `-ldflags="-X .../envelope.buildTime=2026-04-26T12:00:00Z"`;
    dev builds without ldflags degrade to no-op. 11 tests.
  - **Daemon wiring** — `cmd/alf-daemon/crl.go` (`setupCRL`)
    + `internal/capability/envelope/release_key.go` (`go:embed`
    wrapper around `release_pubkey.minisign`) + `cmd/alf-release-keygen/`
    (one-shot keygen tool). Boot path: `CheckBootClock` →
    `MonitorClockSkew` goroutine → if release pubkey embedded AND
    `ALF_CRL_URL` set, start `Refresher.Run()` against
    `wasmRt.TrustStore`. Degrades gracefully when either is
    absent (operator-set `Revoke` still works); only clock-sanity
    refusal escalates to `log.Fatal`.
  - **Stage 2 deferred**: D2 (provider cascade — depends on #392),
    D8 (`alf trust revoke` CLI — depends on #395 Stage 2 admin
    boundary).
- `TestThreeTierAlignment_E2E` (8 subtests) — a single integration
  test that loads two co-resident signed manifests through one
  `Instantiator` (events bus + cross-flow registry wired the way the
  daemon wires them) and exercises the cross-tier behaviour the
  per-package tests can't cover alone: §3.1 FS scope isolation
  across caps, §3.3 declared cross-flow delivery + scope rejection +
  orphan-subscriber denial, §3.2 absence of memory handle.
- `technical/AUDIT-3-TIERS-2026-04-26.md` — alignment audit between
  the implementation on `release/0.8.0` and
  `docs/ARCHITECTURE-SECURITY.md` §3. Catalogues 14 drifts (P1–P3),
  cites file:line evidence, and tracks resolution status.

### Documentation

- `docs/ARCHITECTURE-SECURITY.md` §9 hard-rules table — phantom
  archtest entries (`TestMemoryImplPrivate`,
  `TestEventsBusImplPrivate`) annotated as deferred per audit findings
  D3 + D14, mirroring the existing "tracked by #392" pattern. The
  rules themselves remain in force as code-review discipline; the
  static archtests land alongside #392 (capability providers).

---

## [0.7.9] — 2026-04-22 — Foundation rework

**Zero new user-facing feature. Clean base. Five unified blocks. WASM-ready.**

The 0.7.x cycle surfaced a recurring class of bug: conversational state
dispersed across four packages, `convID` scoping absent from signatures,
model fallbacks hard-coded in a dozen places. 0.7.9 reorganises `internal/`
around five first-class concerns so that each bug class has a single place
to land instead of twelve.

### Architecture

- `internal/` now exposes five canonical blocks — `capability/`, `memory/`,
  `ai/`, `sandbox/`, `runtime/` — with dependency rules enforced by CI
  (`internal/archtest/`). See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).
- Ops-plumbing packages regrouped under `internal/platform/`: `eventlog`,
  `gittrack`, `media`, `mood`, `session`, `signal`, `supervisor`, `tlsgen`,
  `trace`, `updater`, `vulncheck`.
- `internal/secrets/` renamed to `internal/envsecrets/` to remove the
  naming collision with `internal/sandbox/secrets/`.

### Changed

- `ConsolidatePreferences` moved from `internal/memory/` to
  `internal/memory/curation/`; the `memory/` root no longer imports
  `ai/provider`. Consolidation takes a `curation.PrefInvoker` closure so
  curation stays provider-agnostic.
- `ResolveModel` is now the single source of truth for model selection;
  a CI test (`TestHardcodedModelFallback`) fails on any hard-coded
  fallback introduced elsewhere.
- `CurrentConvID` is persisted as a user preference (`memory.Store.GetPref`),
  not a memory-state field. Every conversation-scoped API takes `convID`
  as an explicit parameter.
- `handler_tiers.go` no longer rescans `tools.d/*.json` on every
  `GET /api/tiers` — removes a hot-path disk scan.
- Runtime absorbs provider pass-throughs, streaming (`ConverseStream`),
  per-call options (`ChatRequest`), retry + fallback, and the
  `processStandard` happy path of the legacy chat pipeline (#340 series).

### Removed

- Old fused packages deleted: `chatdb`, `conversation`, `memstore`
  (standalone), `firewall`, `vault`, `router`, `provider`, `agents`
  (standalone). Functionality re-homed in the 5 blocks above.

### Documentation

- New `docs/ARCHITECTURE.md` — canonical, versionless reference. The
  5 blocks, their contracts, the dependency rules enforced by CI, the
  "where does my new file belong?" decision tree, and the seven
  acceptance criteria.
- README rewritten around the 5-block model. Positioning updated from
  Claude-only to backend-agnostic:
  - **CLI backend** — Claude Code (Claude Pro/Max subscription) or
    OpenAI Codex (ChatGPT Plus + GPT-5-codex).
  - **API backend** — any OpenAI-compatible HTTP API (OpenRouter for
    200+ models, OpenAI, Anthropic API, Groq, …).
  - **Local backend** — Ollama.
  Tier-config table adds a `backend` row; `alf login` is scoped to the
  `cli` backend only.
- CONTRIBUTING: architecture patterns rewritten around the 5 blocks,
  with a contributor-facing decision tree, dependency-rules summary,
  and pointer to the archtest CI tests. All stale package paths
  (chatdb, conversation, memstore, firewall, vault, router, provider,
  agents, supervisor→platform/, trace→platform/, signal→platform/,
  tlsgen→platform/) fixed.

### Added

- **User-editable Claude models allowlist** ([#370](https://github.com/alamparelli/alf/issues/370)) —
  `claude_models.txt` is seeded into `configDir` on first run and hot-reloadable.
  Users can add upcoming model IDs without waiting for an ALF release; the
  validator now accepts any string matching the relaxed model-name pattern.
- **`GET /api/models/claude`** — returns the live allowlist as
  `{"models": [...]}` so the Control Center can populate model pickers.
  Edits to `claude_models.txt` fire the `claude_models` SSE event so
  connected clients refresh their dropdowns without a page reload.
- **Model dropdown in tier + router forms** — Control Center fetches
  `/api/models/claude` on mount and renders a `<select>` (visually
  consistent with the rest of the form). The list hot-reloads via SSE.
- User guide for `claude_models.txt` added at
  `internal/controlcenter/docs/claude-models.md`; `tier-setup.md` updated
  with a cross-reference.

### Fixed

- **Classifier stale tier catalog** ([#332](https://github.com/alamparelli/alf/issues/332)) —
  the persistent Claude CLI classifier was started with a generic system
  prompt; tier context lived only in the per-call user prompt. Stale tier
  lists from earlier turns could influence routing after a rename, disable,
  or add. Fix: `BuildSystemPrompt` is now used at startup and
  `UpdateSystemPrompt` is called on every `ReloadTiers`; errors from both
  methods are surfaced instead of swallowed.
- **Daemon reload starved by Telegram long-poll** — reload events sat in
  the channel unprocessed during a 20–30 s `getUpdates` call, so the first
  classification after a profile switch used the stale catalog. Reload
  handling moved to a dedicated goroutine shared by both CC-only and
  Telegram-enabled modes (−133 net lines in `main.go`).
- **Hot-reload router + active sessions on profile switch** — two bugs
  broke the reload path: (1) on API→CLI router transition, a nil
  `cliClassifier` caused the reload branch to silently no-op; (2)
  sessions persisting `ForcedTier` were not validated against the new
  profile's `TierStore.Snapshot()`, forwarding a dangling tier name.
  Both are now corrected.
- **cc_session cookie not refreshed under sliding expiry** —
  `SessionStore.Valid()` extended `expiresAt` server-side past the
  halfway point, but the cookie's `MaxAge` was fixed at login and never
  re-emitted, so browsers dropped the cookie at its original deadline
  and forced a magic-link regen even for active users. `Check()` now
  surfaces the renewal event and `authMiddleware` re-`SetCookie`s
  `cc_session` with a fresh MaxAge on each renewal. `Valid()` is
  read-only (no sliding side-effect) so the rate-limiter's validity
  probe no longer consumes the renewal before the auth path can act
  on it. Verified end-to-end on homelab: authenticated request past
  halfway → `Set-Cookie` header present, `expires_at` bumped by full
  TTL, log line `[CC] session cookie refreshed (sliding expiry, ttl=…)`.

### Security

Fixes for [#385](https://github.com/alamparelli/alf/issues/385) — seven
items landed in this release cycle; two remain for 0.8.0 (bundle signing
and the marketplace audit trail):

- **Vault socket perms tightened** — `vault.sock` chmod changed from
  `0666` to `0660`; `chmod` errors are now logged instead of silently
  discarded.
- **Marketplace registry must use HTTPS** — `NewManager` rejects plain
  `http://` URLs (marketplace disabled, daemon stays alive). Override with
  `ALF_MARKETPLACE_INSECURE=1` emits a loud warning and is accepted; all
  other schemes are rejected.
- **Telegram listener fails closed on empty allowlist** — malformed or
  empty `TELEGRAM_CHAT_ID` values now prevent the bot from starting. An
  empty allowlist previously degraded to "allow any chat ID"; the fix makes
  opt-in explicit with an error log distinguishing the three misconfiguration
  modes.
- **Telegram `/bash` command removed** — the command was gated only on the
  chat-ID allowlist. A leaked bot token plus a known chat ID was sufficient
  for remote shell access. Users needing remote shell should use SSH.
  A regression test (`TestTelegramBashCommandStaysRemoved`) guards against
  re-introduction.
- **Skill entry names validated in `linkAppSkills`** — bundled skill
  directory names are now restricted to `[a-zA-Z0-9_-]+`; entries not
  matching are skipped and logged. `unlinkAppSkills` applies the same filter
  to avoid removing unrelated paths. Naming rules documented in
  `creating-skills.md`.
- **Symlink escape via non-existent path tails blocked** —
  `CheckBoundary`'s "file doesn't exist yet" branch previously used
  `filepath.Clean(Dir(path))` — a lexical operation that never resolves
  symlinks. An app-planted symlink pointing outside the workspace combined
  with a deep non-existent tail could pass the check while the kernel write
  landed outside. `resolveExistingAncestor` now walks up until `Lstat`
  succeeds, calls `EvalSymlinks` on that prefix, and rejoins the
  non-existent tail.

### Dependencies

- Vite bumped to 8.0.9; DOMPurify bumped to 3.4.1.

### Known gaps — tracked for 0.8.0

- [#377](https://github.com/alamparelli/alf/issues/377) — absorb
  `internal/comms/` into `internal/runtime/` (continuation of #340).
- [#378](https://github.com/alamparelli/alf/issues/378) — split
  `internal/controlcenter/` (77 files / 16.6k LoC) into sub-packages
  by domain.
- Sandbox **facet wire-in** (filesystem / network / secrets) — the
  Policy is derived and installed on the context, but the `PolicyFrom`
  lookup at facet enforcement time is still partial. The central
  promise of §2.4 is architecturally in place; completing the wire-in
  across every legacy call-site is scheduled for 0.8.0 alongside the
  marketplace bundle-signing work.

### Verified

- `make regression` — all 40 packages green, global coverage **48.2 %**
  (baseline 47.0 % per [#343](https://github.com/alamparelli/alf/issues/343)).
- Archtest in enforcing mode: `TestFoundationDependencyRules`,
  `TestConsumerDependencyRules`, `TestHardcodedModelFallback` all PASS.
- Manual daemon run on homelab: no panics, no fatals, 24 h chat-job
  success rate 18/18, scheduler and vault-token refresh nominal.

### Notes for upgraders

No user-facing behaviour change is intended. Existing conversations,
memory, apps, schedules, and tier configurations carry forward
unchanged. If you maintain a fork or internal patches, note that the
following import paths have moved:

| Old path                             | New path                                        |
|--------------------------------------|-------------------------------------------------|
| `internal/secrets/`                  | `internal/envsecrets/`                          |
| `internal/supervisor/`               | `internal/platform/supervisor/`                 |
| `internal/updater/`                  | `internal/platform/updater/`                    |
| `internal/tlsgen/`                   | `internal/platform/tlsgen/`                     |
| `internal/vulncheck/`                | `internal/platform/vulncheck/`                  |
| `internal/gittrack/`                 | `internal/platform/gittrack/`                   |
| `internal/trace/`                    | `internal/platform/trace/`                      |
| `internal/eventlog/`                 | `internal/platform/eventlog/`                   |
| `internal/session/`                  | `internal/platform/session/`                    |
| `internal/mood/`                     | `internal/platform/mood/`                       |
| `internal/signal/`                   | `internal/platform/signal/`                     |
| `internal/media/`                    | `internal/platform/media/`                      |
| `memory.ConsolidatePreferences`      | `curation.ConsolidatePreferences` (closure API) |
| `memory.preferencesFile`             | `memory.PreferencesFile` (exported)             |
| `memory.consolidateThreshold`        | `memory.PreferencesThreshold` (exported)        |
