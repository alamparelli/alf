# Contributing to ALF

## Development setup

### Prerequisites

- Go 1.24+
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
# Copy and fill in secrets
mkdir -p dev-secrets
echo "YOUR_BOT_TOKEN" > dev-secrets/telegram_bot_token
echo "YOUR_CHAT_ID" > dev-secrets/telegram_chat_id
openssl rand -hex 16 > dev-secrets/cc_auth_token

# Build and start
docker compose up --build
```

### Deploy to a remote host

```sh
# Requires SSH access to the target machine
./scripts/dev-deploy.sh
```

### Local development (Docker, no remote deploy)

```sh
# Builds Docker image locally and restarts with ALF_IMAGE override
./scripts/dev-local.sh
```

## Code conventions

### Go style

- Standard `gofmt` formatting
- No external dependencies unless absolutely necessary (the project has three: sqlite-vec, go-sqlite3, onnxruntime_go)
- Interfaces at package boundaries, concrete types internally
- Factory pattern for complex object creation (`internal/controlcenter/factory.go`)
- Error messages are lowercase, no punctuation

### Architecture patterns

- **Provider interface** - all LLM interaction goes through `provider.Provider` and `provider.Classifier`
- **Persistent subprocesses** - long-lived processes (classifier) follow the same pattern: mutex, stdin/stdout JSON lines, auto-restart on crash, idle timeout
- **Whisper service** - voice transcription runs in a separate Docker container (`whisper-service`), ALF communicates via HTTP client with bearer token auth
- **Go-native inference** - ONNX embeddings run in-process via `onnxruntime_go` (no sidecar)
- **Embedded core instructions** - `internal/memory/core.md` compiled into the binary via `go:embed`, injected first in every conversation
- **Router is pure logic** - `internal/router/` builds prompts and parses responses, never spawns processes
- **Signal system** - `internal/signal/` provides a Unix-socket server for Claude sessions to send Telegram messages/reactions. System tools (`cmd/signal`, `cmd/schedule-tools`) use this socket
- **Non-root daemon** - Daemon drops to uid 1000 via setpriv with zero capabilities, config is read-only, tools are rx-only

### File organization

- `cmd/` - entry points only, minimal logic (includes system tools: `schedule-tools`, `signal`)
- `internal/` - all business logic, one package per domain
- `scripts/` - deployment, release, and local dev automation (`dev-deploy.sh`, `dev-local.sh`, `release.sh`)
- `internal/controlcenter/web/` - embedded web assets for Control Center (HTML, JS, CSS)

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

Existing system tools: `extract-video` (media processing), `memory-tools` (semantic memory), `schedule-tools` (cron jobs), `signal` (Telegram messaging from Claude sessions).

## Questions

Open an issue on GitHub.
