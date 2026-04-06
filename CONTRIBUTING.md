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

# With FTS5 support (required for memstore)
CGO_ENABLED=1 go build -tags fts5 ./...
```

### Test

```sh
# All tests (except memstore)
go test ./...

# Including memstore (requires CGO + FTS5)
CGO_ENABLED=1 go test -tags fts5 ./...
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

### Architecture patterns

- **Chat engine** - `internal/comms/` owns all message processing logic. Adapters (Telegram, Control Center) call `Process()` and receive events via callbacks
- **Provider interface** - all LLM interaction goes through `provider.Provider` and `provider.Classifier` (CLI + API implementations)
- **Unified conversation store** - `internal/conversation/` captures rich message history (text, tool_use, tool_result, thinking) in a JSONL ring buffer, shared across all backends
- **Chat database** - `internal/chatdb/` provides SQLite-backed persistent message storage
- **Persistent subprocesses** - long-lived processes (classifier) follow the same pattern: mutex, stdin/stdout JSON lines, auto-restart on crash, idle timeout
- **Whisper service** - voice transcription runs in a separate Docker container (`whisper-service`), ALF communicates via HTTP client with bearer token auth
- **Go-native inference** - ONNX embeddings run in-process via `onnxruntime_go`, also exposed as HTTP server (`cmd/embed-server/`)
- **Embedded core instructions** - `internal/memory/core.md` compiled into the binary via `go:embed`, injected first in every conversation
- **Router is pure logic** - `internal/router/` builds prompts and parses responses, never spawns processes
- **Signal system** - `internal/signal/` provides a Unix-socket server for Claude sessions to send Telegram messages/reactions. System tools (`cmd/signal`, `cmd/schedule-tools`) use this socket
- **System tools multi-call binary** - `cmd/system-tools/` bridges CLI tool invocations (task, team, skill, app, config, tier, log, search) to the daemon's HTTP API via symlinks
- **App supervisor** - `internal/supervisor/` manages background services declared in `apps/*/service.json` with restart policies and exponential backoff
- **Outbound firewall** - `internal/firewall/` proxies all HTTP/HTTPS traffic from Claude subprocesses with domain-level allow/deny rules
- **Tracing** - `internal/trace/` logs chain and task team events for observability
- **TLS generation** - `internal/tlsgen/` generates self-signed certificates for local HTTPS installs
- **Non-root daemon** - Daemon drops to uid 1001 (`alfd`) via setpriv, LLM subprocesses run as uid 1000 (`alf`) with zero capabilities and sanitized environment, config is read-only, tools are rx-only

### File organization

- `cmd/` - entry points only, minimal logic (includes system tools: `schedule-tools`, `signal`, `system-tools`, `embed-server`, `nettrack-helper`)
- `internal/` - all business logic, one package per domain
- `scripts/` - deployment, release, and local dev automation (`dev-deploy.sh`, `dev-local.sh`, `ship.sh`)
- `internal/controlcenter/frontend/` - Svelte 5 + Vite frontend, builds to `internal/controlcenter/web/` for `go:embed`
- `internal/controlcenter/web/` - embedded web assets for Control Center (built output, do not edit directly)

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

1. Create `internal/<platform>/` with send/receive logic
2. Add polling or webhook handler in `cmd/alf-daemon/main.go`
3. Wire messages through the existing router - the classification and tier system is platform-agnostic
4. Add platform-specific formatting in the new package (the router returns plain text)

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

- The project follows **SOLID principles** and **factory patterns** - AI agents should maintain these
- **TDD** - write tests first when adding new behavior
- Keep `internal/` packages decoupled - one domain per package, interfaces at boundaries
- System tools in `cmd/` are thin wrappers; business logic stays in `internal/`

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
