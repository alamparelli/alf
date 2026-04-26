# 3-Tier alignment audit — release/0.8.0

**Date:** 2026-04-26
**Scope:** verify the implementation on `release/0.8.0` still matches the design described in `docs/ARCHITECTURE-SECURITY.md` §3.1, §3.2, §3.3.
**Method:** read each tier's spec rules; locate the implementation files; locate the tests / archtests that pin each rule; flag drift.
**Evidence format:** every claim cites `path:line` so the reader can verify without re-walking the tree.

Verdict at a glance:

| Tier | Spec ⇄ code | Pinned by | Net status |
|---|---|---|---|
| 3.1 Structural ocap | matches in shape; one silent functional drop | 12 archtests + per-handle tests | **on track, 1 P1 drift** |
| 3.2 Agent-mediated | structural property holds; active enforcement is partial | 2 archtests + behavioural tests | **on track, 1 P2 gap** |
| 3.3 Private-by-default events | matches spec end-to-end for in-memory bus | 4 loader tests + 7 bus tests | **on track, 1 P2 gap** |

P1 = silent loss of capability declared in manifest.
P2 = behaviour visible in spec but not yet end-to-end through production code paths.

---

## Tier 3.1 — Structural ocap (external I/O + secrets)

### Spec rules (§3.1, §4.2, §4.3)

1. `Runtime.Instantiate` is the only forge of handles.
2. Handle types per resource: `http`, `fs`, `exec`, `secrets`, `tool`, `events.{pub,sub}`. Each handle bakes the caller's identity + scope at forge time.
3. Capabilities receive only handles, never `*Store` / `*Bus` / `*Registry`.
4. Handles are non-serializable (`MarshalJSON` returns error).
5. Revocation through `lifecycleCtx`: `Instance.Close()` cancels in-flight ops, structural not advisory.
6. WASM imports cross-checked against manifest at instantiate time.
7. No `unsafe` / `reflect` / `go:linkname` / `plugin` in capability code.
8. Forge gated by an unforgeable `RuntimeToken` minted exactly once; archtest forbids `MintRuntimeToken` outside `internal/runtime/`.
9. `envelope.Verify` is the single signature-check call site.
10. `sandbox.Identity` carries no authority fields; ctx never carries policy.

### Implementation map

| Rule | Code | Test pinning |
|---|---|---|
| 1, 8 | `internal/capability/handle/forge.go:41` (`MintRuntimeToken` one-shot panic) + `internal/runtime/instantiator.go:128` (`NewInstantiator`) | `TestMintRuntimeTokenIsRuntimeOnly` (`capability_ocap_test.go:28`), `TestForge_SecondMintPanics` (`forge_test.go:65`) |
| 2 | `internal/capability/handle/{fs,http,exec,secrets,tool,events}.go` | per-handle scope + revocation tests (10+ each) |
| 3 | narrow interfaces `EventPublisher` / `EventSubscriber` / `ToolInvoker` consumed by handle package; concrete `*Bus` not exposed in handle types | discipline + the deferred archtest at §9 row "tracked by #392" |
| 4 | every handle has `MarshalJSON() ([]byte, error)` returning `ErrHandleNonSerializable`; e.g. `fs.go:91`, `http.go:173`, `events.go:115`/`198` | static: `TestAllHandleTypesNonSerializable` (`handle_hygiene_test.go:176`); behavioural: `TestFSHandle_NonSerializable`, `TestHTTPHandle_NonSerializable`, etc. |
| 5 | `Instance.Close` cancels `lifecycleCtx`, flips per-handle `revoked` atomic, calls `EventSub.revoke` cleanups (`instance.go:100`); HTTP merges via `context.AfterFunc` (`http.go:24`) | `TestInstantiator_CloseRevokesAllHandles` (`runtime/instantiator_test.go:183`) + per-handle revocation tests |
| 6 | `internal/runtime/wasm/import_check.go` runs at `Instantiate` time; archtest pins host-FS memory access | `TestWASMHostFSUsesMemoryReadWriteOnly` (`wasm_test.go:81`), `TestWazeroImportConfinedToWASMPackage` (`wasm_test.go:21`) |
| 7 | `TestNoUnsafeInCapabilityCode` (`handle_hygiene_test.go:69`), `TestHandlePackageNoUnsafeOrLinkname`, `TestWASMPackageNoUnsafeOrLinkname`, `TestNoPluginStdlibImport` | (themselves) |
| 9 | `internal/runtime/instantiator_verified.go:45` — sole `envelope.Verify` call site outside the envelope package | `TestOneVerifyCallSite` (`capability_ocap_test.go:163`) |
| 10 | `IdentityFrom(ctx)` only; `PolicyFrom`, `policyCtxKey`, `WithPolicy` forbidden | `TestNoPolicyFromCtx`, `TestSandboxIdentityHasNoAuthorityFields`, `TestMarketplaceHasPermissionNotUsedAsSandboxEnforcement` (`sandbox_facets_test.go`) |
| executor scope | `tooling.Executor` importers locked to a curated allowlist | `TestExecutorImportScopePinned` (`executor_scope_test.go:61`) |

### Drifts

**D1 — P1 — `[[fs.writes]]` silently dropped at the forge layer.** **RESOLVED 2026-04-26.** `InstantiateVerified` now forges `FSHandle` directly from the typed `envelope.Manifest.FS`, with both `Reads` and `Writes` populated. Pinned by `TestInstantiateVerified_FSWritesRouted` (`internal/runtime/instantiator_verified_test.go`).

  Original finding (kept for history): The MANIFEST-SCHEMA documents `[[fs.writes]]` as a valid declaration (`docs/MANIFEST-SCHEMA.md:118`, `:204`, `:235`, `:309`). `envelope.Manifest.FS.Writes` is parsed (`internal/capability/envelope/types.go:48`). `FSHandle.scope.Writes` and `alf_fs_write` host import are wired (`internal/capability/handle/fs.go:122`, `internal/runtime/wasm/host_fs.go:131`). But `permissionsFromEnvelope` (`internal/runtime/instantiator_verified.go:147`) routes only `Reads` into the legacy `PermissionSet.FilePaths`, and `forgeGrants` (`instantiator.go:175`) builds `FSScope{Reads: m.Permissions.FilePaths}` with `Writes: nil`. **Net effect:** any capability that declares `[[fs.writes]]` parses + verifies + loads, but every `alf_fs_write` call returns `ErrOutOfScope` regardless of declared paths. The manifest is being read but the declaration is silently ignored.

  Fix shape: extend `permissionsFromEnvelope` to populate a write set, and either grow `capability.PermissionSet` with a `Writes` slice or short-circuit the legacy shim and let the envelope manifest reach `forgeGrants` directly. Issue worth opening to track.

**D2 — P3 — `internal/runtime/instantiator.go` legacy `Instantiate` defaults to `nopVerifier{}` (`instantiator.go:36`).** Production daemons use `InstantiateVerified` and the archtest enforces single Verify call site, so this is unused in production but still callable from tests + legacy code. Not a security drift today, but the asymmetric "two forge entry points, one verifying, one not" surface should be removed once the migration window closes (§12 calls out the legacy path as transitional). Track or remove with #389 Stage 2.

**D3 — P3 — §9 archtest table claims tests that don't exist.** **ANNOTATED 2026-04-26.** §9 table now reads "not yet enforced — tracked by #392 (audit D3)" for the two phantom rows, mirroring the pattern already used for the broader `*Store` / `*Bus` / `*Registry` rule. The static archtests themselves land with #392 (capability providers).

  Original finding (kept for history): `docs/ARCHITECTURE-SECURITY.md:822-823` lists `TestMemoryImplPrivate` (no `*memory.storeImpl` outside `memory/`) and `TestEventsBusImplPrivate` (no `*events.busImpl` outside `events/`) as enforcing rules. Neither test exists in `internal/archtest/`. `events.New()` returns `*Bus` (exported); any package can construct one. Today's only consumers (`cmd/alf-daemon/wasm.go`, `internal/runtime/wasm/loader.go`, the test files) are correct by code review, but no archtest pins that. Either the spec table needs the same "tracked by #392" annotation it already uses for one row, or the tests need to land. Cheapest fix is the doc annotation.

**D4 — P3 — ToolHandle / `WithToolInvoker` not wired into the production daemon.** `instantiator.go:74` has the optional `invoker` field; `instantiator_verified.go:106` forges `ToolHandle` only when an invoker is wired. `cmd/alf-daemon/main.go` does not call `runtime.WithToolInvoker(...)`. `[[tools.declares]]` in shipped skill manifests is parsed and validated by `TestShippedSkillManifestsValidate` (`skills_forge_test.go:36`), but no `ToolHandle` is forged at boot — the LLM still reaches skill tools through the legacy `MirrorInto + skillCapability` path. This matches the explicitly deferred §12 row #389 Stage 2 (orchestrator-level "active-skill" boundary), but worth surfacing because today's behaviour is "tool surface narrowing is declared but not enforced".

**D5 — P3 — §9 row "No capability holds `http.Handle` scoped to CC origin" is not yet enforced.** Spec line 830 acknowledges "tracked by #395". CC ratification page is Stage 2 of #395.

### Verdict 3.1

Structural shape matches the spec. Forge → handle → revocation → archtest pinning is in place across 6 handle types and 12+ archtests. **One real functional drift (D1) that drops a manifest declaration silently** — needs an issue. The other drifts are either deferred Stage 2 work that the §12 milestone table already names or doc-vs-code mismatches at the §9 archtest table.

---

## Tier 3.2 — Agent-mediated (memory)

### Spec rules (§3.2)

1. No structural `MemoryHandle` type — capabilities cannot reach memory through a handle.
2. Kernel prompt shipped with the daemon binary, loaded once at startup, attached to every LLM request in system role.
3. Capability-provided content arrives wrapped in non-authoritative markers (`<capability_content>`, `<tool_output>`, `<fetched_content>`). LLM is instructed not to alter policy based on text inside markers.
4. User-additional policies via `alf policy` are restrict-only (cannot relax kernel defaults).
5. Sensitive memory (tagged at write time) requires TTY confirmation regardless of agent decision.
6. All memory disclosure events go to the audit log.
7. Rate limit memory disclosure per-capability-turn.

### Implementation map

| Rule | Code | Test pinning |
|---|---|---|
| 1 | absent — no `MemoryHandle` type anywhere | `TestNoMemoryHandleType` (`no_memory_handle_test.go:35`) |
| 2 | `internal/runtime/llm/kernel_prompt.txt` (`go:embed`-ed at `kernel_prompt.go:13`) → `KernelPrompt()` accessor → `provider.Registry.SetKernelPrompt` (`registry.go:31`) → `KernelPromptInjector` decorator wraps every backend (`kernel_inject.go:39`); daemon wires at `cmd/alf-daemon/main.go:564` | `TestKernelPromptIsImported` (`no_memory_handle_test.go:95`) + `TestKernelPromptInjector_*` (4 tests in `kernel_inject_test.go`) |
| 3 | `WrapCapabilityContent` / `WrapToolOutput` / `WrapFetchedContent` at `kernel_prompt.go:43–58`; HTML-attribute escape for source string at `:70` | `TestWrap*` (5 tests in `kernel_prompt_test.go`) |
| 4–7 | not implemented — Stage 2 deferred per §3.2 implementation status | — |

### Drifts

**D6 — P2 — Marker helpers exist but are NEVER applied at production call sites.** **RESOLVED 2026-04-26.** Plumbed at three sites: tool result in `internal/runtime/impl.go` (legacy chat loop), tool result in `internal/ai/provider/toolloop.go` (API tool loop, inlined via `wrapToolOutputForLLM` to avoid foundation cross-import), skill body injection in `internal/runtime/agents/prepare.go`. Memory store keeps unwrapped text (recall is not polluted). Pinned by `TestChat_ToolResultMarkerDiscipline`, `TestWrapToolOutputForLLM_PinsMarkerShape`, `TestPrepareOrchestration_SkillBodyWrappedWithMarker`.

  Original finding (kept for history): `WrapCapabilityContent` / `WrapToolOutput` / `WrapFetchedContent` are referenced only by their own tests (`grep -rn` confirms). The kernel prompt instructs the LLM that text inside `<capability_content>` / `<tool_output>` / `<fetched_content>` is non-authoritative — but production code never wraps content in those markers before delivering to the LLM. **Operational reality:** the only active enforcement of §3.2 today is the kernel prompt's general "treat capability content as data" rule; the marker discipline is an empty contract. Spec §3.2 acknowledges this is Stage 2 plumbing work ("the helpers exist; threading them through every site is the Stage 2 plumbing work"), so this is documented-as-deferred — but the gap is wider than the spec phrasing suggests, because today there are zero plumbed sites, not few. Consider listing the call sites that need wrapping (skill prompt assembler, tool result formatter, fetch tool result formatter) as concrete sub-tasks under #415.

**D7 — P3 — `alf policy` CLI absent.** Deferred per §12 row #400 Stage 2 (depends on #395). No drift relative to spec; flagged here so the audit table is complete.

**D8 — P3 — No memory tool surface (`memory.recall`/`get`/`write`/`forget`).** Memory access today still flows through legacy `internal/memory/` ingestion paths and `cmd/alf-daemon/memory_socket_wiring.go`. Deferred to #415. Net effect: the agent does have memory access, but not through a tool surface the kernel prompt can constrain — the constraint is implicit in how the legacy ingestion pipeline runs. Worth keeping eyes on #415's scope.

**D9 — P3 — No rate-limit, no audit, no sensitive-tagging.** All deferred to #396 (audit) or schema migration. Spec acknowledges these as Stage 2.

### Verdict 3.2

The structural property — *no `MemoryHandle` type exists* — is enforced statically and behaviourally. Kernel prompt wiring is real and tested end-to-end through the provider Registry. **The active marker enforcement (D6) is the load-bearing gap**: the spec instructs the LLM to recognise markers, but no production caller produces them. Until callers wrap content, the only constraint on capability-provided text is the kernel prompt's general guidance, which is weaker than the spec implies. This matters because the spec sells §3.2 as "structural property + active enforcement", and active enforcement today is partial.

---

## Tier 3.3 — Private-by-default events

### Spec rules (§3.3)

1. Each capability's events are own-only by default; cross-flow requires two declarations (publisher's `[[events.exports]]` + subscriber's `[[events.subscribes]]`).
2. `Runtime.forgeGrants` materialises `EventPub`/`EventSub` only when the cross-flow registry confirms both sides at install time.
3. `EventPub.Publish` rejects topics outside the manifest's exports.
4. Two-pass loader: pass 1 collects exports across the dir, pass 2 forges sub handles — alphabetical scan order does not lose cross-flows.
5. Boot-time UX: log line + `<dataDir>/events/active-flows.json` snapshot per cross-flow.
6. Cross-flow surfaced at install for user consent.
7. Per-topic rate limits prevent flood DoS.
8. Audit log entries on publish/deliver.
9. Removing a cross-flow (uninstall publisher or edit subscriber manifest) terminates the link.

### Implementation map

| Rule | Code | Test pinning |
|---|---|---|
| 1, 2, 3 | `EventPub.Publish` rejects via `ErrTopicNotExported` (`handle/events.go:79`); `Instantiator.InstantiateVerified` only forges `EventSub` when `crossFlow.HasExport(...)` returns true (`instantiator_verified.go:84`) | `TestEventPub_RejectsTopicNotExported` (`events_test.go:51`), `TestBus_PrivateByDefault_NoSubscribeForUndeclaredFlow` (`bus_test.go:38`), `TestLoader_SubscriberWithoutPublisherSkipped` (`loader_events_test.go:130`) |
| 4 | `wasm.Loader.LoadDir` two-pass: pass 1 walks every manifest into `CrossFlow.RegisterExport`; pass 2 instantiates with the registry populated | `TestLoader_TwoPassResolvesRegardlessOfOrder` (`loader_events_test.go:167`), `TestLoader_CrossFlowForged` (`:74`) |
| 5 | `[events] cross-flow established: <sub> ← <pub>:"<topic>"` log; `<dataDir>/events/active-flows.json` snapshot via `events.WriteSnapshot` | `TestWriteSnapshot_*` (5 tests in `snapshot_test.go`); log line asserted in `TestLoader_CrossFlowForged` |
| 9 | `Instance.Close` calls `EventSub.revoke()`, which runs the bus cleanup func — closing the queue channel and removing the route entry; subscriber's next `Receive` returns `ErrRevoked` | `TestEventSub_RevokedAfterClose` (`events_test.go:109`), `TestBus_CleanupRemovesSubscription` (`bus_test.go:81`), `TestBus_RevocationClosesQueue` (`bus_test.go:133`) |
| slow subscriber | non-blocking publish; full queue → drop event for that subscriber, return `ErrSlowSubscriber` | `TestBus_SlowSubscriberDoesNotBlockPublisher` (`bus_test.go:95`) |
| 6, 7, 8 | not implemented — Stage 2 / dependent on #395, #396, follow-up | — |

### Drifts

**D10 — P2 — Interactive ratification UI absent.** The JSON snapshot at `<dataDir>/events/active-flows.json` is written and the boot-time log line surfaces every cross-flow, but nothing reads the snapshot for display. The spec promises "the UI surfaces the cross-capability flow" at install — today the only signal a user has is the boot log. Stage 2 / depends on #395 (CC ratification page). Consistent with the spec's explicit deferral.

**D11 — P3 — No publisher-fingerprint scoping in `from` field.** Spec §3.3 implementation-status section anticipates `from = "alf-marketplace:cap-B"` form; today `from` is a bare cap ID (`envelope.EventSubscription.From string`). Deferred to #392. Means a malicious publisher with the same cap ID could spoof — but cap IDs come from signed manifests, so this is theoretical until marketplace lands.

**D12 — P3 — No per-topic rate limits.** Bus accepts unbounded publish rate. Slow subscriber gets dropped events but no flood protection at the publisher side. Deferred per spec.

**D13 — P3 — No audit on publish/deliver.** Deferred to #396.

**D14 — P3 — No archtest forbidding `*events.Bus` parameters in capability code.** **ANNOTATED 2026-04-26.** Same close-out shape as D3 — §9 table now points to #392 instead of claiming a phantom test.

  Original finding (kept for history): §9 table line 823 mentions `TestEventsBusImplPrivate` but no such test exists (see D3). Today `events.Bus` is exported; capability packages could grab one through `events.New()`. Discipline only; no CI gate. Document or add the test.

### Verdict 3.3

Structural core is end-to-end: own-only by default, two-pass loader resolves regardless of scan order, deny-by-default verified, revocation cascade through `lifecycleCtx`, snapshot file written. **The biggest remaining gap (D10) is UX, not structural** — every signed cross-flow IS pre-approved by the user (they signed both manifests), but there's no install-time interactive surface beyond the log line. This is the explicitly deferred Stage 2 dependency on #395.

---

## Cross-cutting observations

### Composition attacks (§5)

Spec acknowledges the residual: two caps with an *intentionally* declared cross-flow can compose unintendedly. Current state: no IFC, no taint, no transitive provenance. This is named in §5 and not a drift.

### Admin boundary (#395 Stage 1)

`internal/admin/pending` ships the package marker + `Store` contract + in-memory implementation + `TestAdminPackageBoundary` archtest. **Not yet wired.** No daemon code calls `pending.Store.Append` from a ratification-required path; no CLI command (`alf pending`, `alf ratify`) consumes it. The boundary is pinned but the queue is dormant. Stage 2 lights it up. Worth tracking that today's Tier 3.2 kernel prompt instruction "queue it for user ratification" has nowhere to queue to.

### Revocation end-to-end (#396)

`Instance.Close` revocation works inside one cap's lifetime (D5 fully working for in-flight ops, D9 working for cross-flow termination on uninstall). Missing pieces from §8:
- Cascade across providers ("revoke provider → close all dependents atomically") — no provider feature yet (#392).
- Key-based revocation — trust store has add/remove, but no "revoke key X → invalidate every bundle X signed" walk.
- Timestamp binding — bundle envelope has signing timestamp, but no `not-valid-after-time` enforcement against a CRL.
- Offline CRL behaviour with N-day fail-safe — entirely #396 territory.

This is the single open gate for v0.8.0-beta in §12.

### Doc-vs-code drift in §9 hard-rules table

Two listed archtests do not exist (D3, D14). Cheapest fix: edit the §9 table to add "tracked by #392" or "deferred" annotations next to those rows, mirroring the pattern already used for "No capability package takes `*Store` / `*Bus` / `*Registry`".

---

## Recommended follow-ups

Listed by priority, not bundled into a plan — each is a discrete decision point.

| ID | Drift | Suggested action | Cost |
|---|---|---|---|
| D1 | `[[fs.writes]]` silently dropped | **DONE 2026-04-26**: forge `FSHandle` directly from envelope `FS.Reads`+`FS.Writes` in `InstantiateVerified`. | resolved |
| D6 | Marker helpers unused at every call site | **DONE 2026-04-26**: wrapped at 3 production sites (`impl.go`, `toolloop.go`, `prepare.go`). | resolved |
| D3, D14 | §9 table claims non-existent archtests | **DONE 2026-04-26**: annotated as deferred to #392 (mirrors the pattern already used for the broader `*Store`/`*Bus`/`*Registry` rule). Static archtests land with #392. | resolved |
| D10 | No interactive ratification surface | Spec already defers to #395 Stage 2 — confirm the JSON shape suffices for that page; no work today. | 0 |
| D2, D4 | Two forge entry points + ToolHandle not wired | Roll into #389 Stage 2 close-out. | (already scoped) |
| #396 | Revocation end-to-end | Only open v0.8.0-beta gate. | (already scoped) |

---

## Summary

The three-tier model is **structurally honest**: each tier delivers what the spec promises about its enforcement shape — Tier 3.1 cryptographic + structural, Tier 3.2 agent-judgment, Tier 3.3 declared-flow. The archtest network is dense (15+ rules across 7 files) and the per-handle behavioural coverage is real. Two real drifts deserve attention: D1 (silent loss of a declared capability) and D6 (active marker enforcement is empty in practice). Everything else in the audit either matches spec or is explicitly named as Stage 2 deferred work in §12.

If the goal is to ship v0.8.0-beta with a clean alignment story, address D1 (fast) and clean up the §9 table phantom tests (faster), then turn to #396. D6 is bigger but the spec already defers it to Stage 2 of #400 / #415, and v0.8.0-beta acceptance does not block on it.
