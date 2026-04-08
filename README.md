# ALF

A personal AI assistant that lives in your Telegram chats. Built in Go. Runs on your hardware.

ALF connects Claude to your messaging, wraps it with semantic long-term memory, configurable response tiers, voice transcription, and a web dashboard - all in a single Docker container you control.

## Why ALF

Most AI assistant frameworks are Node.js monoliths with hundreds of dependencies. ALF is different:

- **Go binary, zero JS runtime** - single static binary, minimal attack surface, no `node_modules` supply chain
- **Semantic memory** - Go-native ONNX embeddings (sqlite-vec + FTS5) for real long-term recall, not just context window tricks
- **Persistent classifier** - a long-lived Claude process handles message routing in ~0ms instead of spawning a new process per message
- **Tier system** - configurable response tiers (model, tools, effort, read/write access) routed by an LLM classifier
- **Defense-in-depth security** - Non-root daemon (uid 1001), LLM subprocess isolated as uid 1000 with zero capabilities, read-only config, restricted tool execution
- **Multi-backend** - Claude CLI, OpenRouter, OpenAI, Ollama, or any OpenAI-compatible API. Mix backends per tier
- **No API costs** - runs on your Claude subscription (Pro/Max/Team), not pay-per-token API calls
- **Self-hosted** - your hardware, your data, your rules. No cloud dependency beyond your Claude account

## How it works

![ALF OS Architecture](docs/architecture.jpeg)

**Host CLI** (`alf`) manages the container lifecycle from the host machine. Inside Docker, the **daemon** runs as `alfd` (uid 1001) and serves the Control Center web UI, polls Telegram, and coordinates all subsystems. LLM subprocesses run as `alf` (uid 1000) with restricted permissions and zero capabilities. A sidecar container handles voice transcription (faster-whisper).

Messages flow through a **chat engine** (`internal/comms/`) → **router** → **LLM provider** pipeline. The router classifies intent and selects a response tier (model, tools, effort level). A **permission system** gates access to sandboxed apps and tools. Apps run in chroot-isolated namespaces with filesystem allowlists. Secrets are accessed exclusively through a **vault proxy** (Unix socket) — no direct access from app code. A **tracing system** (`internal/trace/`) logs chain and task events for observability.

## Quick start

### Prerequisites

- Docker and Docker Compose
- 2 GB RAM minimum
- *Optional:* a [Telegram bot token](https://core.telegram.org/bots#how-do-i-create-a-bot) (via @BotFather) + your chat ID
- *Recommended:* a Claude, Codex, or OpenAI-compatible API subscription (configured via the Setup Wizard)

### Install

```sh
curl -fsSL install.alfos.ai | sh
```

### Setup

```sh
alf init
```

The interactive wizard walks you through:
1. Choosing an install directory
2. Configuring Telegram credentials
3. Setting up dashboard access (HTTP or HTTPS with Let's Encrypt)
4. Starting the container
6. Authenticating Claude

After the container is running, the Control Center **Setup Wizard** guides you through backend selection (Claude CLI, OpenRouter, OpenAI, Ollama), tier presets, and optional Telegram configuration - all from the browser.

### After setup

```sh
alf login     # Authenticate Claude inside the container
alf status    # Check container health
alf logs      # Tail container logs
```

Send a message to your bot on Telegram. That's it.

## Features

### Semantic memory

Alf remembers things across conversations. The memory system uses sqlite-vec for vector similarity search and FTS5 for keyword matching, with Go-native ONNX Runtime inference (all-MiniLM-L6-v2) - no Python dependency for embeddings.

Alf has three memory tools:
- **recall** - hybrid semantic + keyword search over past memories
- **remember** - store facts, preferences, decisions, summaries
- **forget** - remove memories by ID

Memories are automatically recalled when relevant to the current conversation. A batch extractor periodically distills conversation logs into new memories.

### Core instructions

Operational knowledge (Docker environment, filesystem layout, tool discovery, Telegram formatting rules) is compiled into the binary via `go:embed` and injected into every conversation. User-editable files (`soul.md`, `index.md`) handle personality and preferences only - clean installs behave correctly out of the box.

### Configurable tiers

Define response tiers with different capabilities:

| Property | What it controls |
|----------|-----------------|
| `model` | Claude model (haiku, sonnet, opus) |
| `effort` | Thinking effort (low, medium, high) |
| `tools` | Available tools for the session |
| `write_capable` | Whether Claude can modify files |
| `max_turns` | Agentic loop depth |
| `instant` | Router responds directly (no second LLM call) |

The LLM classifier reads your message and picks the right tier. Greetings get instant haiku responses. Complex tasks get multi-turn opus sessions with full tool access.

### Multi-backend support

ALF isn't limited to Claude. Connect OpenRouter (200+ models), OpenAI (GPT-4), Ollama (local models), or any OpenAI-compatible API. Mix backends in the same tier configuration - route simple messages to a free model and complex tasks to Claude. Conversation context flows seamlessly across backend switches.

### Voice transcription

Send a voice message on Telegram. ALF transcribes it via the whisper-service container and processes the text as a regular message. The whisper-service runs faster-whisper and is deployed as a separate Docker container, keeping the main ALF image lean.

### Media processing

- **Images** - forwarded to Claude's vision capabilities
- **Videos/GIFs** - frame extraction into contact sheets + audio transcription
- **PDFs** - text extraction via pdftotext
- **Documents** - passed through to Claude with appropriate context

### Skills & tools

ALF discovers CLI tools and skills at boot. System tools live in `tools.d/` (read-only), user tools in `tools/`. Same pattern for skills (`skills.d/`, `skills/`). All tools support `--help` - ALF runs it before first use. Missing a capability? Drop an executable in `tools/` or a skill definition in `skills/`.

### Agent teams

ALF can coordinate multiple specialized agents for complex tasks. An **orchestrator** (Opus) breaks down requests, delegates sub-tasks to agents (researcher, writer, reviewer...), reviews results, and synthesizes a final answer. Agents work in isolated sessions - no context bleeds between them.

Teams are defined as JSON files in `config.d/agents/`. A bundled "starter" team ships with ALF. Invoke by asking ALF to "use agents" or "lance les agents", or schedule with `--tier agent`.

### Scheduler

Cron-based job scheduling with timezone support. Four execution modes:

- **LLM jobs** - run a prompt through any tier (`--tier sonnet --prompt "..."`)
- **Direct jobs** - execute bash commands (`--tier direct --command "df -h"`)
- **Orchestrator jobs** - coordinate multiple agents (`--tier orchestrator --prompt "..."`)
- **Reminders** - send a message directly to Telegram (`--message "Stand up!"`)

Configurable per-job timeouts. Execution logs recorded for every run. Daily digest summarizes job results. Skill injection via `--skills` flag.

### Control Center

Web dashboard at port 8080 with sidebar navigation:

- **Chat** - web-based chat with SSE streaming, media upload, reactions, multiple conversation tabs, markdown rendering
- **Home** - workspace file explorer, teach (memory ingestion), admin actions
- **Terminal** - interactive shell session inside the container
- **Tasks** - monitor and launch agent tasks, approve/reject, delete completed tasks
- **Schedules** - create, edit, and monitor scheduled jobs with execution logs and filters
- **Logs** - daemon logs with search and session-based filtering
- **Tiers** - configure response tiers in real-time
- **Firewall** - network firewall rules for outbound access (log-only or enforce mode)
- **Vault** - secrets vault with OAuth2 browser flow, file storage, service credentials
- **Settings** - configuration editor, Telegram setup, backend registration
- **Docs** - built-in documentation
- **Apps** - self-contained apps generated by ALF with optional background services, auto-discovered in the sidebar

Supports dark/light themes (default, catppuccin, sage) and a mobile-first responsive layout with bottom navigation bar. Authentication via Telegram magic link (`/login` command) or bearer token with session cookies. Issuing a new magic link revokes all previous sessions. IP ban after repeated auth failures (configurable threshold and duration).

### Conversation engine

A unified conversation store captures rich message history (text, tool calls, tool results, thinking blocks) in a JSONL ring buffer. Context is preserved across backend switches - switching from a CLI tier to an API tier mid-conversation carries history forward automatically.

### Force commands & session locking

Tiers with `force_command: true` can be invoked directly (e.g., `/opus analyze this`). The session locks to that tier for all subsequent messages until `/new` or session timeout. You can also lock without sending a message (`/opus` alone).

### Reaction-based learning

Emoji reactions on messages (positive or negative) trigger behavioral learning. Negative reactions prompt ALF to ask what went wrong and extract learnings. Positive reactions reinforce good behaviors.

### App framework

ALF can generate self-contained web apps in `data/apps/`. Each app gets a sidebar entry, CSP-sandboxed serving, and optional background services supervised by the daemon (auto-restart on crash, exponential backoff).

### Onboarding

First-time users get an automatic onboarding prompt injected into their first conversation. The `/start` Telegram command re-triggers it. The Control Center Setup Wizard guides backend and tier configuration from the browser.

### Daily mood

ALF has a rotating personality layer - 16 moods (sharp, philosophical, sardonic, playful, grumpy, zen, detective...) that cycle based on the date. This affects tone, not capability.

## CLI reference

```
alf init          Interactive setup wizard (Docker setup, secrets)
alf start         Start the container
alf stop          Stop the container
alf restart       Restart the container
alf upgrade       Pull latest image and restart (update alias works too)
alf login         Authenticate Claude inside the container
alf status        Show container status and versions
alf logs          Tail container logs
alf secret        Manage secrets (list, set, remove)
alf magic-link    Generate a Control Center login link
alf token         Print the Control Center bearer token
alf token reset   Generate a new bearer token
alf compose       Regenerate docker-compose.yml from saved profile
alf uninstall     Remove ALF and its data
alf version       Print version
```

## Project structure

```
cmd/
  alf/             Host CLI (init, start, stop, upgrade, login, secret, compose)
  alf-daemon/      Container daemon (Telegram bot + Control Center + Claude management)
  embed-server/    Embedding HTTP server (ONNX inference for vector search)
  extract-video/   System tool: video frame extraction + audio transcription
  memory-tools/    System tool: recall, remember, forget (semantic memory)
  nettrack-helper/ Privileged conntrack helper (conntrack events → Unix socket)
  schedule-tools/  System tool: create, list, delete, update scheduled jobs
  signal/          System tool: send Telegram messages and reactions from Claude sessions
  system-tools/    Multi-call binary bridging CLI tools to daemon HTTP API

internal/
  agents/          Multi-agent orchestrator, team config store, session isolation
  chatdb/          SQLite-backed chat message database
  cli/             CLI command implementations + embedded templates + bundled skills
  comms/           Chat engine: message processing pipeline, adapters, event dispatch
  controlcenter/   HTTP server, auth, config CRUD, chat API, workspace, setup wizard, docs
  conversation/    Unified conversation store (JSONL ring buffer, ContentBlocks, cross-backend)
  marketplace/     App marketplace: install, permissions, trust model, manifest validation
  provider/        Provider/Classifier interfaces + CLI + API implementations
  router/          LLM-based message classification + tier routing
  memstore/        Semantic memory (SQLite + sqlite-vec + FTS5 + ONNX embedder)
  media/           Download, MIME detection, frame extraction, contact sheets, PDF parsing
  voice/           HTTP client for whisper-service transcription container
  scheduler/       Cron-based job scheduling with timezone support + execution logging
  supervisor/      App background service supervisor (restart policies, exponential backoff)
  vault/           Vault-proxy subprocess management + Unix socket proxy
  firewall/        Outbound HTTP/HTTPS traffic filtering proxy
  tlsgen/          Self-signed TLS certificate generation for local installs
  tooling/         Tool registry + subprocess executor + Linux namespace sandbox
  trace/           Tracing and event logging for chains and task teams
  skills/          Skill loader, trigger matching, catalog builder
  mood/            Daily mood rotation + live feedback + reaction learning
  session/         Claude session persistence (resume IDs, forced tier locking)
  telegram/        Telegram Bot API client + Markdown→HTML
  memory/          System prompt assembly (embedded core + soul, mood, context, onboarding)
  signal/          Unix-socket server for Claude→Telegram message delivery
  gittrack/        Git versioning for data directory
  eventlog/        JSONL event logging with daily rotation
  updater/         GHCR image update checker
  secrets/         Docker secrets reader
  vulncheck/       Dependency vulnerability checking
```

## Security model

The entrypoint runs as root for package installation and permission setup, then drops to `alfd` (uid 1001) via `setpriv` with minimal capabilities (setuid, setgid, sys_admin, sys_chroot, chown). LLM subprocesses run as `alf` (uid 1000) with zero capabilities and a sanitized environment:

- **User separation** - daemon runs as `alfd` (uid 1001), LLM runs as `alf` (uid 1000) with allowlist-only environment
- **Filesystem isolation** - `/opt/alf/config.d/` read-only, `/opt/alf/tools.d/` read+execute only, `/home/alf/data/` read+write
- **Secrets** - Docker secrets mechanism, never in environment variables
- **Security headers** - HSTS, X-Frame-Options DENY, CSP with SRI on CDN dependencies, X-Content-Type-Options nosniff
- **Rate limiting** - 60 req/min global (120 for authenticated users), 5 req/min on auth, CORS on the Control Center API
- **Authentication** - magic link (time-limited, rotating) or bearer token with session cookies
- **Session revocation** - new magic link invalidates all previous sessions
- **IP ban** - after repeated auth failures (configurable threshold and duration)
- **SSRF protection** - vault-proxy blocks requests to private/link-local IP ranges with DNS-level validation
- **Outbound firewall** - HTTP/HTTPS proxy with allow/deny rules per domain
- **Container signing** - all Docker images are signed with [Cosign](https://github.com/sigstore/cosign) using keyless signing via GitHub OIDC, with SBOM attestations (SPDX-JSON)

### Verify image signatures

```bash
# Install cosign
brew install cosign

# Verify alf image
cosign verify ghcr.io/alamparelli/alf:<version> \
  --certificate-identity-regexp="github.com/alamparelli/alf" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"

# Verify whisper-service
cosign verify ghcr.io/alamparelli/whisper-service:<version> \
  --certificate-identity-regexp="github.com/alamparelli/alf" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"

# Verify SBOM attestation
cosign verify-attestation ghcr.io/alamparelli/alf:<version> \
  --certificate-identity-regexp="github.com/alamparelli/alf" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  --type spdxjson
```

## Requirements

- **OS**: Linux or macOS (Docker required)
- **RAM**: 512 MB minimum (2 GB recommended for voice transcription)
- **Disk**: ~800 MB for the Docker image + ~600 MB on first voice message (Whisper model)
- **Network**: outbound HTTPS to Telegram API and Claude API

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, code conventions, and guidelines.

## License

[MIT](LICENSE)
