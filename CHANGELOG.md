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
