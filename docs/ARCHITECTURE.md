# ALF Architecture

> This is the canonical reference for ALF's internal structure.
> Every refactor, every new feature, every PR review points back to this document.
> Golden rule: **if a new package fits none of the 5 blocks nor the periphery, it's a red flag — open a discussion before coding.**

---

## 1. One-sentence description

ALF is:

> A **Capability** (tool / skill / app) is executed by the **AI**,
> with graded access to **Memory**,
> inside a **Sandbox** that enforces a Policy.
> The **Runtime** orchestrates the four.

Anything that does not directly participate in that sentence is **periphery** (plumbing, transport, UI, ops).

---

## 2. The 5 blocks

```
internal/
├── capability/   ← what ALF can execute       (tools + skills + apps)
├── memory/       ← what ALF knows / remembers (conv + embeddings + preferences)
├── ai/           ← the brain that decides     (provider + strategy + ResolveModel)
├── sandbox/      ← the guards that enforce    (firewall + vault + filesystem + integrity)
└── runtime/      ← the conductor              (orchestrates the four)
```

### 2.1 `capability/`

**Role.** Everything ALF can execute on the AI's behalf.

**Absorbs.** `tooling/` + `skills/` + `marketplace/` (as Capability producers; the adapters live in those source packages and register into `capability.Registry`).

**Minimum contract.**

```go
type Capability interface {
    Manifest() Manifest                          // name, version, description, kind
    Permissions() PermissionSet                  // what it declares it needs
    Execute(ctx context.Context, in Input) (Output, error)
}

type Kind int
const (
    KindTool  Kind = iota // native short-lived execution (read_file, bash, grep...)
    KindSkill             // prompt + orchestrated tools (commit-push, doc-writer...)
    KindApp               // UI iframe + backend (xpost, contacthive...)
)
```

**Hard rules.**
- A Capability **never calls** another Capability directly. It returns a result; the Runtime composes.
- A Capability **does not know** about Memory nor AI. Inputs arrive via `Input`.
- Every Capability has a versioned Manifest and an explicit PermissionSet.
- The Marketplace is a **Capability registry** (index + installation).

### 2.2 `memory/`

**Role.** Unified persistence for everything ALF knows.

**Sub-packages.**
- `memory/dedup/` — near-duplicate blocking at index time.
- `memory/embed/` — concrete ONNX implementation of `memory.Embedder` (tokenizer included). Used by `cmd/embed-server`.
- `memory/curation/` — fact extraction from LLM output (`Extractor`) + periodic consolidation/dedup (`Consolidator`). Also hosts `ConsolidatePreferences`, which was moved out of the memory root so the root stays free of `ai/provider` imports. These services **consume** `memory.Store`; they do not redefine the contract.
- `memory/memtest/` — contract tests run against every `Store` implementation.
- `memory/socketsrv/` — IPC server for the `memory-tools` binary.

**Minimum contract.**

```go
type Store interface {
    // Conversations
    AppendMessage(ctx, convID ConvID, msg Message) error
    ListMessages(ctx, convID ConvID, opts ListOpts) ([]Message, error)
    Summarize(ctx, convID ConvID) (Summary, error)

    // Embeddings
    Index(ctx, scope Scope, doc Document) error
    Search(ctx, scope Scope, query string, k int) ([]Hit, error)

    // Preferences
    GetPref(ctx, key string) (Value, error)
    SetPref(ctx, key string, val Value) error
}
```

**Hard rules.**
- **Every function that touches a conversation takes `convID` as an explicit parameter.** No "current conv" hidden in a singleton.
- One SQLite schema. `CurrentConvID` / `ActiveConvID` is a user **preference**, not a memory-state field.
- Conv scoping is checked at the function signature, not in the body.
- Embeddings and messages share the same `convID` when they are tied to a conv.

### 2.3 `ai/`

**Role.** Everything that turns an intent into tokens.

**Internal organisation.** `internal/ai/` at the root holds the public contract (`Engine`, `Strategy`, `Request`, `Event`, `ResolveModel`, `Message`, `ToolCall`, `ToolSpec`). Implementations live in private sub-packages: `ai/provider/` holds the concrete drivers (API / CLI / Codex / Registry).

**Minimum contract.**

```go
type Engine interface {
    Run(ctx context.Context, req Request) (<-chan Event, error)
}

type Request struct {
    Model       ModelID        // resolved by ResolveModel, never hard-coded
    Messages    []Message      // supplied by Memory, not re-read by AI
    Tools       []Capability   // supplied by Runtime, not picked by AI
    MaxTokens   int
    Stream      bool
}

// Strategy describes how to chain multiple Engine.Run calls for a single
// turn (retry, chain-of-thought, parallel, single-shot, ...). Runtime picks
// the Strategy; ai/ owns the contract, not the orchestration machinery
// (sessions, task store, spawning — those live in runtime/).
type Strategy interface {
    Run(ctx context.Context, engine Engine, req Request) (<-chan Event, error)
}
```

**Hard rules.**
- **One `ResolveModel`, everywhere.** A CI test (`TestHardcodedModelFallback`) fails when a hard-coded fallback sneaks into any other package.
- AI **does not read** Memory directly. Runtime prepares the Request.
- AI **does not execute** Capabilities. It emits `ToolCall` events; Runtime runs them.
- `ai/` owns the `Strategy` contract. Concrete orchestrators (agents, classifier router) live in `runtime/`.

### 2.4 `sandbox/`

**Role.** Enforce per-Capability access policies.

**Sub-packages.**
- `sandbox/exec/` — chroot + setpriv + per-app namespaces for executing Capabilities.
- `sandbox/integrity/` — hash-based tool/script integrity (TOCTOU-safe via exec-time hash).
- `sandbox/network/` — firewall proxy + outbound allow/deny rules.
- `sandbox/secrets/` — vault + per-app proxy socket. **Note:** this is distinct from `internal/envsecrets/` (the env-var / Docker-secret reader at boot).

**Minimum contract.**

```go
type Policy struct {
    FileAccess  FileRules    // read/write paths authorised
    Network     NetworkRules // domains authorised
    Secrets     SecretRules  // vault keys accessible
    Tier        Tier         // aggregated capabilities
}

type Sandbox interface {
    // Returns a ctx that cap.Execute() must run inside.
    Apply(ctx context.Context, cap Capability, policy Policy) (context.Context, error)
}
```

**Hard rules.**
- **One Policy applies to one Capability.** No implicit accumulation.
- The Policy is **derived from the Capability's Manifest** plus the user tier. Never ad hoc.
- Firewall (network), Vault (secrets), Filesystem (exec) and Integrity are **four facets** of the same Sandbox.
- Integrity scanning runs **inside** the Sandbox, not alongside it.
- **This is the central promise of ALF.** Enforcement is staged — integrity is wired today; facet wire-in (network/secrets/filesystem through `PolicyFrom(ctx)`) is the v0.8.0 follow-up.

### 2.5 `runtime/`

**Role.** Orchestrate the four other blocks. The **only** package that knows them all.

**Sub-packages.**
- `runtime/agents/` — multi-agent orchestrator (absorbed from the old `agents/` package).
- `runtime/classifier/` — tier classifier (absorbed from the old `router/` classifier).

**Minimum contract.**

```go
type Runtime interface {
    // Process one user chat turn.
    Chat(ctx context.Context, convID memory.ConvID, userInput string) (<-chan Event, error)

    // Execute a one-shot Capability (UI button, scheduler, ...).
    Invoke(ctx context.Context, capID CapabilityID, args Args) (Output, error)

    // Streaming variants (Converse / ConverseStream) for chat pipelines.
}
```

**What Runtime does.**
1. Resolves the Capability from the registry.
2. Loads history from Memory (scoped by convID).
3. Derives the Policy from the Manifest + tier.
4. Calls `Sandbox.Apply` to prepare the ctx.
5. Runs `AI.Run` with the prepared Request.
6. Loops on `ToolCall` events: executes each Capability via Sandbox, re-injects results.
7. Persists the turn into Memory.

**Hard rules.**
- Runtime is the **only** package that imports `capability + memory + ai + sandbox` together.
- None of the four blocks imports Runtime.
- Consumers (controlcenter, telegram, scheduler) import **only** Runtime.

---

## 3. Periphery

These packages do not participate in the core sentence. They are consumers or plumbing.

### User-facing consumers
- `cli/` — host CLI
- `controlcenter/` — web UI
- `telegram/` — Telegram bot
- `voice/` — voice transcription bridge
- `scheduler/` — cron-based invoker (consumer of `Runtime.Invoke`)

### Ops / plateform (`platform/`)
Grouped under `internal/platform/` to make the block-vs-periphery separation visible:

- `platform/eventlog/` — append-only event log
- `platform/gittrack/` — git repo watcher (extraction signal)
- `platform/media/` — image/video/PDF processing
- `platform/mood/` — daily personality layer
- `platform/session/` — CLI session bookkeeping
- `platform/signal/` — Unix-socket server for in-sandbox Telegram/reaction calls
- `platform/supervisor/` — background service manager (restart policies)
- `platform/tlsgen/` — self-signed cert generator for local HTTPS
- `platform/trace/` — chain/task tracing
- `platform/updater/` — image-update notifier
- `platform/vulncheck/` — `govulncheck` wrapper (CI guard)

### Ancillary (still at `internal/` root)
- `envsecrets/` — Docker / Kubernetes env-var secret reader at boot. Distinct from `sandbox/secrets/`.
- `marketplace/` — app installer + permission gate + lifecycle manager. Registers apps as Capabilities via `capability_adapter.go`.
- `skills/` — skills FS store + prompt catalog + trigger matching. Registers skills as Capabilities via `capability_adapter.go`.
- `tooling/` — tool discovery (scan `tools.d/*.json`), native-tool host, integrity integration. Registers tools as Capabilities via `capability_adapter.go`.
- `comms/` — legacy chat pipeline. Its absorption into `runtime/` is tracked in issue #377 (v0.8.0).
- `archtest/` — CI-only package that enforces dependency rules.

---

## 4. Dependency rules

```
consumers (controlcenter, telegram, scheduler, cli, ...)
    │
    ▼
  runtime
    │
    ├──► capability
    ├──► memory
    ├──► ai
    └──► sandbox
```

**Hard forbidden imports:**
- `capability` **must not** import memory, ai, sandbox, runtime.
- `memory` **must not** import capability, ai, sandbox, runtime.
- `ai` **must not** import capability, memory, sandbox, runtime.
- `sandbox` **must not** import capability (it takes a `Manifest` as parameter), memory, ai, runtime.
- **Only runtime** may import the four.
- No consumer imports one of the four blocks directly (apart from public types re-exposed via runtime).

A CI test enforces these rules (`internal/archtest/deps_test.go` — `TestFoundationDependencyRules`, `TestConsumerDependencyRules`). It runs in enforcing mode.

A second CI test (`TestHardcodedModelFallback`) fails when any file outside `ai/resolve.go` introduces a model fallback string.

---

## 5. Decision tree: where does my new file belong?

Use this when adding a new file or package:

1. **Is it executable by the AI (a tool, skill, or app)?**
   → `capability/` contract + register through the adapter in `tooling/`, `skills/`, or `marketplace/`.

2. **Does it persist or recall data (conversations, embeddings, preferences)?**
   → `memory/` (or a sub-package under `memory/`).

3. **Does it turn an intent into tokens (provider driver, strategy, model resolution)?**
   → `ai/` (contract) or `ai/provider/` (concrete driver).

4. **Does it enforce a policy (file access, network, secrets, integrity)?**
   → `sandbox/` (or a facet sub-package).

5. **Does it orchestrate the four (pipelines, agents, classifier)?**
   → `runtime/`.

6. **Is it a user-facing surface (CLI, web UI, Telegram, voice, scheduler)?**
   → root-level consumer package (`cli/`, `controlcenter/`, `telegram/`, `voice/`, `scheduler/`).

7. **Is it ops plumbing (cron, tracing, supervision, updater, cert gen, event log, …)?**
   → `platform/<name>/`.

8. **None of the above?** → 🚨 Red flag. Open a discussion issue before you code.

---

## 6. Acceptance criteria

The architecture is honoured when:

1. The 5 packages (`capability`, `memory`, `ai`, `sandbox`, `runtime`) exist and respect the dependency rules (CI-verified).
2. Old fused packages (`chatdb`, `conversation`, `memstore`, `firewall`, `vault`, `router`, `provider`, `agents` standalone) no longer exist.
3. No user-facing behaviour change from the refactor (regression tests green).
4. Average PR diff on a bug fix is under 3 files (measured on the next fix cycle).
5. `ResolveModel` is unique and protected by a CI test.
6. `CurrentConvID` is a preference read from Memory, not a memory-state field.
7. A new contributor can point to one of the 5 blocks for 80 % of "where do I touch for X?" questions.

---

## 7. What this architecture is NOT

- ❌ A rewrite. It is a reorganisation — the business code stays, it changes address.
- ❌ A place to add new features without going through the decision tree.
- ❌ A static document — when reality diverges from this text, we change whichever is wrong (usually the text).

## 8. Why it exists

The 0.7.8 cycle produced ~30 fixes, five of them alone on `chat / multi-tab / conv scoping`. The common root: conversational state scattered across four packages (`chatdb`, `conversation`, `memstore`, `memory`) and `convID` scoping absent from function signatures.

Without this rework each cycle repeats the same pattern. With it, the class of bugs disappears by construction.
