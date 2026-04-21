# Changelog

All notable changes to ALF are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Older releases (0.7.8 and earlier) are not retroactively documented here —
see the Git history and GitHub releases for pre-0.7.9 changes.

---

## [0.7.9] — Foundation rework

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
