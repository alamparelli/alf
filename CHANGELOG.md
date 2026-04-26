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
