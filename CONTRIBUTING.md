# Contributing to ALF

## Development setup

### Prerequisites

- Go 1.25+
- Docker and Docker Compose
- CGO enabled (required for sqlite-vec)

### Build

```sh
# All binaries
go build ./...

# With FTS5 support (required for memory embeddings + sqlite-vec)
CGO_ENABLED=1 go build -tags fts5 ./...
```

### Test

```sh
# All tests (except memory sub-packages using FTS5)
go test ./...

# Full regression (CGO + FTS5 required for memory/, sandbox/*, archtest)
CGO_ENABLED=1 go test -tags fts5 ./...

# Or via make:
make regression         # same as above, with coverage report
make regression-quick   # no coverage, faster
```

### Run locally

```sh
# First run: creates dev-secrets/ with placeholders, builds frontend + Docker, starts stack
./scripts/dev-local.sh

# Edit secrets with real values
vim dev-secrets/telegram_bot_token  # your @BotFather token
vim dev-secrets/telegram_chat_id    # your Telegram chat ID

# Restart after editing secrets
./scripts/dev-local.sh
```

The script builds the Svelte frontend, builds the Docker image, and starts the stack. Control Center runs at `http://localhost:8080`.

Useful flags:
- `--no-frontend` — skip Svelte rebuild (faster iteration on Go code)
- `--clean` — tear down existing containers first
- `--down` — stop the stack


## Code conventions

### Go style

- Standard `gofmt` formatting
- No external dependencies unless absolutely necessary (the project has three: sqlite-vec, go-sqlite3, onnxruntime_go)
- Interfaces at package boundaries, concrete types internally
- Factory pattern for complex object creation (`internal/controlcenter/factory.go`)
- Error messages are lowercase, no punctuation

### Architecture — 5 blocks

ALF's code is organised around five first-class concerns, enforced by CI:

```
internal/
├── capability/   ← what ALF can execute       (tools + skills + apps)
├── memory/       ← what ALF knows / remembers (conv + embeddings + preferences)
├── ai/           ← the brain that decides     (provider + strategy + ResolveModel)
├── sandbox/      ← the guards that enforce    (firewall + vault + filesystem + integrity)
└── runtime/      ← the conductor              (orchestrates the four)
```

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the full reference, sub-package layout, and contract interfaces. What follows is the contributor-facing summary.

#### Where does my new file belong?

Use this decision tree when adding a new file or package:

1. **Is it executable by the AI (a tool, skill, or app)?**
   → `internal/capability/` contract + register through the adapter in `tooling/`, `skills/`, or `marketplace/`.

2. **Does it persist or recall data (conversations, embeddings, preferences, facts)?**
   → `internal/memory/` (or a sub-package: `memory/embed/`, `memory/curation/`, `memory/dedup/`, …).

3. **Does it turn an intent into tokens (provider driver, strategy, model resolution)?**
   → `internal/ai/` (contract) or `internal/ai/provider/` (concrete driver).

4. **Does it enforce a policy (file access, network, secrets, integrity)?**
   → `internal/sandbox/` (or a facet sub-package: `sandbox/exec/`, `sandbox/network/`, `sandbox/secrets/`, `sandbox/integrity/`).

5. **Does it orchestrate the four (pipelines, agents, classifier)?**
   → `internal/runtime/` (or `runtime/agents/` for multi-agent, `runtime/classifier/` for tier selection).

6. **Is it a user-facing surface (CLI, web UI, Telegram, voice, scheduler)?**
   → root-level consumer package: `cli/`, `controlcenter/`, `telegram/`, `voice/`, `scheduler/`.

7. **Is it ops plumbing (cron, tracing, supervision, updater, cert gen, event log, …)?**
   → `internal/platform/<name>/`.

8. **None of the above?** → 🚨 **Red flag.** Open a discussion issue before you code. If a new concept doesn't fit any of the five blocks nor the periphery, either the concept is ill-framed or the architecture needs an explicit amendment — both deserve a conversation first.

#### Dependency rules (enforced by CI)

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

Forbidden imports:

- `capability` **must not** import `memory`, `ai`, `sandbox`, `runtime`.
- `memory` **must not** import `capability`, `ai`, `sandbox`, `runtime`.
- `ai` **must not** import `capability`, `memory`, `sandbox`, `runtime`.
- `sandbox` **must not** import `capability`, `memory`, `ai`, `runtime`.
- **Only `runtime` may import the four blocks together.**
- No consumer imports an inner block directly — everything goes through `runtime`.

Enforcement lives in `internal/archtest/deps_test.go` (`TestFoundationDependencyRules`, `TestConsumerDependencyRules`). Both run in enforcing mode — a violation fails CI. A third test (`TestHardcodedModelFallback`) ensures `ai.ResolveModel` stays the single source of truth for model selection.

Justified cross-block imports (e.g. `marketplace` → `capability` for adapter types) are listed as exceptions in the archtest and must be documented when added.

#### Patterns worth knowing

- **Capability adapters.** `tooling/`, `skills/`, and `marketplace/` each keep a `capability_adapter.go` that wraps their native objects (NativeTool / Skill / App) as `capability.Capability`. They register into `capability.Registry` via dual-registration. Runtime resolves Capabilities through the unified registry.
- **Memory contract tests.** Any new `memory.Store` implementation must pass `internal/memory/memtest/` — the contract test battery guarantees scoping + idempotency invariants.
- **Provider interface.** All LLM interaction goes through `ai.Engine` + `ai.Strategy`. Provider drivers implement `ai.Engine` inside `ai/provider/`. The runtime picks the Strategy.
- **ONNX embeddings.** `memory/embed/` wraps `onnxruntime_go` in-process. `cmd/embed-server/` exposes the same embedder over HTTP for cases where CGO is unavailable.
- **`go:embed` core prompts.** Operational prompts (`core.md`, `onboarding.md`, …) live in `internal/memory/` and are compiled into the binary via `go:embed` in `memory/prompts.go`.
- **Persistent subprocesses.** Long-lived processes (classifier) share a pattern: mutex, stdin/stdout JSON lines, auto-restart on crash, idle timeout.
- **Whisper sidecar.** Voice transcription runs in a separate Docker container (`whisper-service`); ALF talks to it over HTTP with bearer-token auth.
- **Signal socket.** `internal/platform/signal/` is a Unix-socket server that lets in-sandbox Capabilities send Telegram messages / reactions. `cmd/signal` and `cmd/schedule-tools` are the clients.
- **System-tools multi-call binary.** `cmd/system-tools/` bridges CLI subcommands (task, team, skill, app, config, tier, log, search) to the daemon's HTTP API via symlinks.
- **App supervisor.** `internal/platform/supervisor/` manages background services declared in `apps/*/service.json` with restart policies + exponential backoff.
- **Outbound firewall.** `internal/sandbox/network/` proxies all HTTP/HTTPS traffic from LLM subprocesses with domain-level allow/deny rules.
- **Tracing.** `internal/platform/trace/` logs chain and task-team events for observability.
- **Non-root daemon.** Daemon drops to uid 1001 (`alfd`) via `setpriv`; LLM subprocesses run as uid 1000 (`alf`) with zero capabilities and sanitised environment. Config is read-only, tools are rx-only.

### File organization

- `cmd/` - entry points only, minimal logic (includes system tools: `schedule-tools`, `signal`, `system-tools`, `embed-server`, `nettrack-helper`, `extract-video`, `memory-tools`)
- `internal/` - all business logic, organised into the 5 blocks + periphery (see decision tree above)
- `scripts/` - deployment, release, and local dev automation (`dev-deploy.sh`, `dev-local.sh`, `ship.sh`)
- `internal/controlcenter/frontend/` - Svelte 5 + Vite frontend, builds to `internal/controlcenter/web/` for `go:embed`
- `internal/controlcenter/web/` - embedded web assets for Control Center (built output, do not edit directly)
- `docs/` - user- and contributor-facing documentation (including `ARCHITECTURE.md`)
- `technical/` - implementation notes, test baselines, archived specs

### Testing

- Tests live next to the code they test (`foo.go` → `foo_test.go`)
- Use table-driven tests where it makes sense
- Mock at interfaces, not at implementation details
- `internal/controlcenter/` has the most comprehensive test coverage - use it as a reference

## Making changes

### Before you start

1. Check existing issues to avoid duplicate work
2. For large changes, open an issue first to discuss the approach

### Commit messages

Use conventional-style messages:

```
feat: add WhatsApp channel support
fix: classifier fails on empty messages
refactor: extract media pipeline from daemon
```

Keep the subject line under 72 characters. Add a body if the "why" isn't obvious from the diff.

### Pull requests

- One feature or fix per PR
- Include tests for new behavior
- Ensure `go build ./...` and `go test ./...` pass
- Update README if you're adding user-facing features

## Adding a new messaging platform

ALF currently supports Telegram. To add a new platform:

1. Create `internal/<platform>/` with send/receive logic (same level as `telegram/`, `voice/`).
2. Add polling or webhook handler in `cmd/alf-daemon/main.go`.
3. Consume `runtime.Runtime.Chat(ctx, convID, input)` — the classification and tier system is platform-agnostic and lives inside Runtime. Consumers do not call `ai/` / `memory/` / `capability/` directly.
4. Add platform-specific formatting in the new package (Runtime returns plain text + structured events).

## Adding a new tool

There are two ways to add tools - one requires rebuilding the image, the other doesn't.

### User tools (no rebuild)

Drop any executable (script, binary) into the `data/tools/` directory on your host. It's a mounted volume, so changes are immediate - no Docker rebuild needed.

```sh
# Example: add a shell script tool
cat > /path/to/alf/data/tools/my-tool << 'EOF'
#!/bin/bash
echo "Hello from my tool"
EOF
chmod +x /path/to/alf/data/tools/my-tool
```

User tools are auto-discovered at boot and listed in Claude's toolbox. Claude runs them via the Bash tool.

### System tools (image rebuild)

System tools are Go binaries baked into the Docker image at `/opt/alf/tools/`. Use this path when contributing a tool to the ALF project itself:

1. Create `cmd/<tool>/main.go`
2. Add build step in `Dockerfile`:
   ```dockerfile
   RUN go build -o /<tool> ./cmd/<tool>
   ```
3. Copy to tools directory:
   ```dockerfile
   COPY --from=builder /<tool> /opt/alf/tools/<tool>
   ```
4. The daemon symlinks system tools into `data/tools.d/` at startup

Existing system tools: `extract-video` (media processing), `memory-tools` (semantic memory), `schedule-tools` (cron jobs), `signal` (Telegram messaging from Claude sessions), `system-tools` (multi-call binary bridging CLI to daemon API), `embed-server` (embedding HTTP server), `nettrack-helper` (conntrack event logger).

## AI-assisted development

ALF is built with heavy AI assistance. Here's how to work effectively with AI coding agents on this codebase.

### Working with AI agents

- Point the agent at [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) first — the 5-block model + decision tree is what keeps generated code in the right place.
- The project follows **SOLID principles** and **factory patterns** — AI agents should maintain these.
- **TDD** — write tests first when adding new behaviour.
- Keep `internal/` packages decoupled — enforce the block boundaries from `internal/archtest/` in every PR.
- System tools in `cmd/` are thin wrappers; business logic stays in `internal/`.

### Frontend (Svelte)

The Control Center frontend lives in `internal/controlcenter/frontend/` (Svelte 5 + Vite). After changes:

```sh
cd internal/controlcenter/frontend && npm run build
```

Built output goes to `internal/controlcenter/web/` which is `go:embed`-ded into the binary. Do not edit files in `web/` directly.

### Skills and apps

- **Skills** are defined in `skills.d/` (bundled) or `skills/` (user). They are plain text definitions with trigger patterns
- **Apps** use vanilla JS + AlfSDK (not frameworks) due to CSP restrictions (`unsafe-eval` blocked). See the `sdk-app-builder` skill for guidance
- Apps run in CSP-sandboxed iframes with no direct access to secrets (vault-proxy only)

### Security considerations

When contributing code that touches:
- **Subprocess execution** - always use the `alf` user (uid 1000), never root
- **Secrets** - use `readSecret()` pattern (Docker secrets), never environment variables
- **Network** - outbound requests from LLM subprocesses go through the firewall proxy
- **App isolation** - apps run in chroot namespaces with filesystem allowlists
- **Vault access** - apps access secrets via vault-proxy Unix socket, never directly

## Questions

Open an issue on GitHub.
