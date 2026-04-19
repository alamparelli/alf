# TEST-BASELINE — milestone 0.7.9

The regression safety net for the milestone 0.7.9 foundation rework
(#343). Every ticket in this milestone MUST run `make regression`
green before it starts and before it merges. No architectural move
is accepted without this suite passing on both ends.

## Green baseline

| Field | Value |
|---|---|
| Branch | `release/0.7.9` |
| Commit SHA | `96c7e6de878079511465e4fae32dc027400720c0` |
| Timestamp (UTC) | `2026-04-19T16:23:51Z` |
| `go test ./...` | PASS (0 failing packages) |
| Package count | 39 (31 with tests, 8 without) |
| Global coverage | **40.2 %** of statements |
| Build flags required | `CGO_ENABLED=1 -tags fts5` |

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

Packages are grouped by whether they are in the **step 1–4 move
path** of the rework. Steps 1–4 consolidate memory, capabilities
(tooling/skills/marketplace), sandbox (firewall/vault/integrity) and
ai (provider/router/agents) — those are the packages whose public
surface must NOT shift behaviour. Green baseline is the authority
on "no user-facing change".

### In the move path (critical)

| Package | Coverage | Rework target | Critical gap to watch |
|---|---:|---|---|
| `internal/conversation` | 88.4 % | memory | multi-conv isolation, convID scoping |
| `internal/memstore` | 55.8 % | memory | dedup + FTS fallback under load |
| `internal/memory` | 41.9 % | memory | preferences dispatch, recall-tools |
| `internal/chatdb` | 31.1 % | memory | convID scoping on every write/read |
| `internal/tooling` | 59.5 % | capability | e2e per native tool (~20) + integrity guard |
| `internal/skills` | 75.7 % | capability | skill dispatch through full pipeline |
| `internal/marketplace` | 48.6 % | capability | app REST backend round-trip |
| `internal/firewall` | 78.9 % | sandbox | nettrack control socket (E2E-only) |
| `internal/vault` | 48.8 % | sandbox | subprocess lifecycle (E2E-only) |
| `internal/provider` | 46.4 % | ai | model routing, summarize pipeline |
| `internal/router` | 62.5 % | ai | `ResolveModel` baseline |
| `internal/agents` | 73.9 % | ai | agent loop, tool invocation |
| `internal/controlcenter` | 47.6 % | runtime consumer | `chat_service` concurrency, session scoping |
| `pkg/appsdk` | 26.4 % | guest SDK | app-side contract stays stable |

### Outside the move path (reference green)

| Package | Coverage |
|---|---:|
| `internal/eventlog` | 85.7 % |
| `internal/signal` | 83.7 % |
| `internal/updater` | 75.7 % |
| `internal/tlsgen` | 72.1 % |
| `internal/telegram` | 71.9 % |
| `internal/gittrack` | 60.9 % |
| `internal/voice` | 58.8 % |
| `internal/trace` | 54.7 % |
| `internal/scheduler` | 48.3 % |
| `cmd/embed-server` | 46.0 % |
| `internal/media` | 41.1 % |
| `internal/session` | 31.4 % |
| `internal/supervisor` | 28.6 % |
| `internal/comms` | 27.1 % |
| `cmd/alf-daemon` | 7.3 % |
| `internal/cli` | 2.9 % |
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
| 1 | Multi-conv isolation (concurrent convs, no state bleed) | partial | `internal/controlcenter` + `internal/conversation` |
| 2 | ConvID scoping on every write/read | gap | `internal/chatdb` |
| 3 | Summarize pipeline (long conv → stored + retrievable) | to verify | `internal/provider` + `internal/memory` |
| 4 | Each native tool has one e2e integration test | gap | `internal/tooling` |
| 5 | One skill through full pipeline | partial | `internal/skills` + `internal/controlcenter` |
| 6 | Marketplace app with REST backend responds | gap | `internal/marketplace` + `pkg/appsdk` |
| 7 | Firewall blocked net fails, allowed succeeds | covered | `internal/firewall` (HTTP e2e via goproxy) |
| 8 | Vault tier-gated access | covered | `internal/vault` (manager api + proxy) |
| 9 | Integrity guard quarantines tampered tool | to verify | `internal/tooling` (`integrity_test.go`) |
| 10 | `ResolveModel` baseline behaviour | to verify | `internal/router` |
| 11 | Scheduler job fires → invokes capability | gap | `internal/scheduler` + `internal/tooling` |

Gap-filling plan is deliverable 3 — tracked in a follow-up inside
this ticket.

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
detector. It is **not** part of the baseline contract because the
audit surfaced a pre-existing race in
`internal/agents/orchestrator.go:432` — tests
`TestRun_PlanWithValidation_*` and `TestRun_Arbitration*` read a
field that `Orchestrator.Run` writes from another goroutine without
synchronisation. The production build does not run with `-race`
either, so the race has not shipped a user-visible regression, but
it must be fixed before the ai rework starts (step 4). Tracked in
#345. Until then, `regression-race` is the tech-debt tracker.

## Entry / exit contract for follow-up tickets

Every ticket in milestone 0.7.9 (#344+) links to this file and
states, in its acceptance section:

> Entry: `make regression` green on HEAD.
> Exit: `make regression` green on HEAD of the ticket branch, with
> coverage for the moved packages not below the baseline in the
> table above.
