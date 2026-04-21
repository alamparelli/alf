# TEST-BASELINE — milestone 0.7.9

The regression safety net for the milestone 0.7.9 foundation rework
(#343). Every ticket in this milestone MUST run `make regression`
green before it starts and before it merges. No architectural move
is accepted without this suite passing on both ends.

## Green baseline

| Field | Value |
|---|---|
| Branch | `release/0.7.9` |
| Commit SHA | `35e3e60d3d77f7e5da3f72c1616d3de95ffd003d` |
| Timestamp (UTC) | `2026-04-21T12:45:45Z` |
| `go test ./...` | PASS (0 failing packages) |
| Package count | 49 (40 with tests, 9 without) |
| Global coverage | **48.2 %** of statements |
| Build flags required | `CGO_ENABLED=1 -tags fts5` |
| Race suite (`make regression-race`) | PASS (0 data races) |

Rebaselined after #340 (Step 4: AI + Runtime) closed on 2026-04-21 — see
#371 for the drift analysis this rebaseline resolves. Coverage rose
from 40.2 % → 48.2 % because the runtime + ai + sandbox sub-packages
introduced in Steps 3–4 carry high-coverage test suites (`runtime` at
84.5 %, `capability` at 100 %, `sandbox/integrity` at 82.8 %) while
the absorbed packages (`provider`, `router`, `agents`, `firewall`,
`vault`) retired.

Regenerate with:

```bash
make regression
```

### Why the build flags matter

`internal/memstore` creates an FTS5 virtual table at migration time
(`CREATE VIRTUAL TABLE memory_fts USING fts5(…)`). The
`github.com/mattn/go-sqlite3` driver only embeds FTS5 when compiled
with `CGO_ENABLED=1` and the `fts5` build tag. Running the vanilla
`go test ./...` makes every `memstore` test fail with
`no such module: fts5` and yields a ~32 % false coverage. The
Dockerfile already builds the production `alf-daemon` with these
flags — the regression command matches prod.

## Per-package coverage

Packages are grouped into the **5 foundation blocks** of the v0.7.10
rework (capability / memory / ai / sandbox / runtime) and everything
else. The foundation blocks are the load-bearing packages whose public
surface must NOT shift behaviour; the rest are consumers or adjacent
utilities.

### Foundation blocks (critical)

| Package | Coverage | Block | Notes |
|---|---:|---|---|
| `internal/capability` | **100.0 %** | capability | Small, well-tested; Manifest / PermissionSet / Registry |
| `internal/memory` | 82.5 % | memory | SQLite backend + InMem implementations; memtest contract runs against both |
| `internal/memory/dedup` | 90.9 % | memory | near-dup blocking + threshold logic |
| `internal/memory/socketsrv` | 64.0 % | memory | IPC for memory-tools binary |
| `internal/memstore` | 67.9 % | memory | legacy extractor/consolidator; Step 1.3 residue tracked in #369 |
| `internal/ai` | **100.0 %** | ai | Contract types + ResolveModel (pure helpers); archtest forbids hardcoded model fallbacks outside this package |
| `internal/ai/provider` | 55.7 % | ai | APIProvider / CLIProvider / CodexProvider / ToolLoop (CLI subprocesses are E2E-only) |
| `internal/sandbox` | 97.0 % | sandbox | Sandbox interface + Policy derivation |
| `internal/sandbox/exec` | 82.4 % | sandbox | subprocess sandbox (linux/other) |
| `internal/sandbox/integrity` | 82.8 % | sandbox | hash-based tamper detection |
| `internal/sandbox/network` | 78.9 % | sandbox | nettrack + proxy (absorbed from firewall) |
| `internal/sandbox/secrets` | 48.8 % | sandbox | vault manager + proxy (absorbed from vault) — subprocess lifecycle E2E-only |
| `internal/runtime` | **84.5 %** | runtime | Chat / Invoke / Converse / ConverseStream orchestrator |
| `internal/runtime/agents` | 71.7 % | runtime | agent orchestrator (absorbed from internal/agents) |
| `internal/runtime/classifier` | 61.2 % | runtime | intent classifier (absorbed from internal/router) |

### Capability implementations (tooling / skills / marketplace)

| Package | Coverage | Notes |
|---|---:|---|
| `internal/tooling` | 72.0 % | 16 native tools + integrity guard |
| `internal/skills` | 76.1 % | Mirrors into capability.Registry as KindSkill |
| `internal/marketplace` | 70.7 % | Mirrors into capability.Registry as KindApp (legacy install path covered by SEC-001 bundle) |

### Consumers

| Package | Coverage | Notes |
|---|---:|---|
| `internal/comms` | 39.2 % | ChatEngine, processStandard (now Runtime-backed) |
| `internal/controlcenter` | 50.9 % | chat_service + handlers; handler_avatar covered |
| `internal/scheduler` | 59.6 % | executor + engine + CommandCapability |
| `pkg/appsdk` | 51.2 % | app-side SDK contract |

### Outside the move path (reference green)

| Package | Coverage |
|---|---:|
| `internal/session` | 97.0 % |
| `internal/mood` | 89.5 % |
| `internal/trace` | 88.3 % |
| `internal/eventlog` | 85.7 % |
| `internal/signal` | 83.7 % |
| `internal/updater` | 75.7 % |
| `internal/tlsgen` | 72.1 % |
| `internal/telegram` | 71.9 % |
| `internal/media` | 62.2 % |
| `internal/gittrack` | 60.9 % |
| `internal/voice` | 58.8 % |
| `cmd/embed-server` | 46.0 % |
| `internal/supervisor` | 28.6 % |
| `cmd/alf-daemon` | 13.2 % |
| `internal/cli` | 2.9 % |
| `internal/archtest` | no statements (architectural tests) |
| `internal/vulncheck` | no statements |

### No tests at all

| Package | Kind | Action |
|---|---|---|
| `cmd/alf` | main CLI wiring | deferred — thin entrypoint |
| `cmd/extract-video` | command wrapper | deferred |
| `cmd/memory-tools` | command wrapper | deferred |
| `cmd/schedule-tools` | command wrapper | deferred |
| `cmd/signal` | command wrapper | deferred |
| `cmd/system-tools` | command wrapper | deferred |
| `internal/mood` | domain | to assess in gap pass |
| `internal/secrets` | **security-sensitive** | 🔴 must cover in gap pass |

## Critical-path scenarios (deliverable 2)

The scenarios below are the regression contract for milestone 0.7.9.
A cross-package test must exist for each before any rework starts.
Gaps identified in the audit:

| # | Scenario | Status | Host package |
|---|---|---|---|
| 1 | Multi-conv isolation (concurrent convs, no state bleed) | covered | `internal/memory` (`memtest` contract `ConvIsolation` + `SQLite_ConcurrentAppend_NoLockedErrors`) |
| 2 | ConvID scoping on every write/read | covered at signature level | every `memory.Store` method takes `convID` as an explicit parameter (#336 rule) |
| 3 | Summarize pipeline (long conv → stored + retrievable) | covered | `internal/memory` (`memtest` contract `AppendSummary_ReplacesCoveredOnApply` + `LatestSummaryCovered`) + `internal/comms/summarize.go` |
| 4 | Each native tool has one e2e integration test | covered | `internal/tooling` (`native_*_test.go`) |
| 5 | One skill through full pipeline | covered | `internal/skills` (catalog/store/inject tests) |
| 6 | Marketplace app with REST backend responds | covered | `internal/controlcenter` (`handler_app_proxy_test.go`) |
| 7 | Firewall blocked net fails, allowed succeeds | covered | `internal/sandbox/network` (HTTP e2e via goproxy, absorbed from internal/firewall) |
| 8 | Vault tier-gated access | covered | `internal/sandbox/secrets` (manager api + proxy, absorbed from internal/vault) |
| 9 | Integrity guard quarantines tampered tool | covered | `internal/sandbox/integrity` (absorbed from internal/tooling/integrity.go) |
| 10 | `ResolveModel` baseline behaviour | covered | `internal/ai` (`resolve_test.go`; router.ResolveModel now a shim — absorbed in R5g) + `internal/archtest/hardcoded_model_test.go` (enforcing, R5f) |
| 11 | Scheduler job fires → invokes capability | covered | `internal/scheduler` (executor + engine tests) |

All critical-path scenarios pinned. Deliverable 3 complete.

## Regression command

The single entry point is:

```bash
make regression
```

It runs `CGO_ENABLED=1 go test -tags fts5 -count=1 ./...` and
writes a coverage profile to `technical/cover.out`. Any step in
milestone 0.7.9 that cannot both enter and exit on a green
`make regression` is rejected.

A `make regression-race` target runs the same suite under the race
detector and is now **part of the baseline contract** alongside
`make regression`. #345 fixed the pre-existing race on `TaskMeta`
(Orchestrator.Run writes vs. Running()/Approve reads) by guarding
every meta mutation with `o.mu` and returning deep copies from
Running(). From this point on, any ticket whose diff introduces a
new data race must either resolve it before merging or file a
tracking issue and leave the race unfixed only with explicit
milestone-owner approval.

## Entry / exit contract for follow-up tickets

Every ticket in milestone 0.7.9 (#344+) links to this file and
states, in its acceptance section:

> Entry: `make regression` AND `make regression-race` green on HEAD.
> Exit: `make regression` AND `make regression-race` green on HEAD
> of the ticket branch, with coverage for the moved packages not
> below the baseline in the table above.
