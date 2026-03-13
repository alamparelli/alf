# ALF

A personal AI assistant that lives in your Telegram chats. Built in Go. Runs on your hardware.

ALF connects Claude to your messaging, wraps it with semantic long-term memory, configurable response tiers, voice transcription, and a web dashboard - all in a single Docker container you control.

## Why ALF

Most AI assistant frameworks are Node.js monoliths with hundreds of dependencies. ALF is different:

- **Go binary, zero JS runtime** - single static binary, minimal attack surface, no `node_modules` supply chain
- **Semantic memory** - Go-native ONNX embeddings (sqlite-vec + FTS5) for real long-term recall, not just context window tricks
- **Persistent classifier** - a long-lived Claude process handles message routing in ~0ms instead of spawning a new process per message
- **Tier system** - configurable response tiers (model, tools, effort, read/write access) routed by an LLM classifier
- **Defense-in-depth security** - Unix user isolation (uid 1001), read-only config, restricted tool execution, not just a container boundary
- **No API costs** - runs on your Claude subscription (Pro/Max/Team), not pay-per-token API calls
- **Self-hosted** - your hardware, your data, your rules. No cloud dependency beyond your Claude account

## How it works

```
Host machine                         Docker container
┌──────────────┐                     ┌──────────────────────────────────┐
│  alf CLI     │  docker compose     │  alf-daemon (PID 1)              │
│              │ ──────────────────► │                                  │
│  init/start/ │                     │  ┌──────────┐ ┌──────────────┐   │
│  stop/upgrade│                     │  │Telegram  │ │Control Center│   │
│              │                     │  │poller    │ │ :8080        │   │
│              │                     │  └────┬─────┘ └──────┬───────┘   │
│              │                     │       ▼              ▼           │
│              │                     │  ┌──────────┐  ┌──────────┐      │
│              │                     │  │Claude    │  │ Whisper  │      │
│              │                     │  │(uid 1001)│  │ (python) │      │
│              │                     │  └──────────┘  └──────────┘      │
└──────────────┘                     └──────────────────────────────────┘
```

**Host CLI** (`alf`) manages the container lifecycle. **Daemon** runs inside Docker, polling Telegram for messages and serving the Control Center web UI on port 8080.

Messages flow through a **router** that classifies intent and selects a response tier. Each tier defines which Claude model to use, what tools are available, and whether write access is granted. Simple messages get instant responses from the classifier itself. Complex requests spawn a full Claude session with the appropriate capabilities.

## Quick start

### Prerequisites

- Docker and Docker Compose
- A [Telegram bot token](https://core.telegram.org/bots#how-do-i-create-a-bot) (via @BotFather)
- Your Telegram chat ID
- A [Claude subscription](https://claude.ai/pricing) (Pro, Max, or Team - no API key needed)

### Install

```sh
curl -fsSL https://raw.githubusercontent.com/alamparelli/alf/main/scripts/install.sh | sh
```

Or build from source:

```sh
go install github.com/alamparelli/alf/cmd/alf@latest
```

### Setup

```sh
alf init
```

The interactive wizard walks you through:
1. Choosing an install directory
2. Configuring Telegram credentials
3. Setting up dashboard access (HTTP or HTTPS with Let's Encrypt)
4. Selecting a JavaScript runtime (Node.js, Deno, Bun, or none)
5. Starting the container
6. Authenticating Claude

### After setup

```sh
alf login     # Authenticate Claude inside the container
alf status    # Check container health
alf logs      # Tail container logs
```

Send a message to your bot on Telegram. That's it.

## Features

### Semantic memory

ALF remembers things across conversations. The memory system uses sqlite-vec for vector similarity search and FTS5 for keyword matching, with Go-native ONNX Runtime inference (all-MiniLM-L6-v2) - no Python dependency for embeddings.

Claude has three memory tools:
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

### Voice transcription

Send a voice message on Telegram. ALF transcribes it locally and processes the text as a regular message. Uses faster-whisper on x86 and whisper.cpp on arm64 - auto-detected at startup, model downloaded on first voice message.

### Media processing

- **Images** - forwarded to Claude's vision capabilities
- **Videos/GIFs** - frame extraction into contact sheets + audio transcription
- **PDFs** - text extraction via pdftotext
- **Documents** - passed through to Claude with appropriate context

### Skills & tools

ALF discovers CLI tools and skills at boot. System tools live in `tools.d/` (read-only), user tools in `tools/`. Same pattern for skills (`skills.d/`, `skills/`). All tools support `--help` - ALF runs it before first use. Missing a capability? Drop an executable in `tools/` or a skill definition in `skills/`.

### Agent teams

ALF can coordinate multiple specialized agents for complex tasks. An **orchestrator** (Opus) breaks down requests, delegates sub-tasks to agents (researcher, writer, reviewer...), reviews results, and synthesizes a final answer. Agents work in isolated sessions - no context bleeds between them.

Teams are defined as JSON files in `config.d/agents/`. A bundled "starter" team ships with ALF. Invoke manually via `/orchestrator <task>` or schedule with `--tier orchestrator`.

### Scheduler

Cron-based job scheduling with timezone support. Four execution modes:

- **LLM jobs** - run a prompt through any tier (`--tier sonnet --prompt "..."`)
- **Direct jobs** - execute bash commands (`--tier direct --command "df -h"`)
- **Orchestrator jobs** - coordinate multiple agents (`--tier orchestrator --prompt "..."`)
- **Reminders** - send a message directly to Telegram (`--message "Stand up!"`)

Configurable per-job timeouts. Execution logs recorded for every run. Daily digest summarizes job results. Skill injection via `--skills` flag.

### Control Center

Web dashboard at port 8080 with sidebar navigation:

- **Chat** - web-based chat with SSE streaming, media upload, reactions
- **Home** - workspace file explorer, teach (memory ingestion), admin actions
- **Terminal** - interactive shell session inside the container
- **Tasks** - monitor and launch agent tasks (orchestrator workflows)
- **Schedules** - create, edit, and monitor scheduled jobs with execution logs
- **Logs** - daemon logs with search and session-based filtering
- **Tiers** - configure response tiers in real-time
- **Firewall** - network firewall rules for outbound access
- **Vault** - secrets vault with OAuth2 browser flow, file storage, service credentials
- **Docs** - built-in documentation
- **Apps** - self-contained apps generated by ALF, auto-discovered in the sidebar

Authentication via Telegram magic link (`/login` command) with session cookies. Issuing a new magic link revokes all previous sessions. IP ban after repeated auth failures (configurable threshold and duration).

### Onboarding

First-time users get an automatic onboarding prompt injected into their first conversation. The `/start` Telegram command re-triggers it. Re-running `alf init` pre-fills previous configuration values (install dir, port, timezone) from a saved setup profile.

### Daily mood

ALF has a rotating personality layer - 16 moods (sharp, philosophical, sardonic, playful, grumpy, zen, detective...) that cycle based on the date. This affects tone, not capability.

## CLI reference

```
alf init          Interactive setup wizard
alf start         Start the container
alf stop          Stop the container
alf restart       Restart the container
alf upgrade       Pull latest image and restart
alf login         Authenticate Claude inside the container
alf status        Show container status
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
  alf/             Host CLI (init, start, stop, upgrade, login)
  alf-daemon/      Container daemon (Telegram bot + Control Center + Claude management)
  extract-video/   System tool: video frame extraction + audio transcription
  memory-tools/    System tool: recall, remember, forget (semantic memory)
  schedule-tools/  System tool: create, list, delete, update scheduled jobs
  signal/          System tool: send Telegram messages and reactions from Claude sessions

internal/
  agents/          Multi-agent orchestrator, team config store, session isolation
  cli/             CLI command implementations + embedded templates
  controlcenter/   HTTP server, auth, config CRUD, chat API, workspace, pages, teach
  provider/        Provider/Classifier interfaces + Claude CLI implementations
  router/          LLM-based message classification + tier routing
  memstore/        Semantic memory (SQLite + sqlite-vec + FTS5 + ONNX embedder)
  media/           Download, MIME detection, frame extraction, contact sheets, PDF parsing
  voice/           Hybrid transcription (faster-whisper x86 / whisper.cpp arm64)
  scheduler/       Cron-based job scheduling with timezone support + execution logging
  vault/           Vault-proxy subprocess management + master password persistence
  mood/            Daily mood rotation + live feedback
  session/         Claude session persistence (resume IDs)
  telegram/        Telegram Bot API client + Markdown→HTML
  memory/          System prompt assembly (embedded core + soul, mood, context, onboarding)
  signal/          Unix-socket server for Claude→Telegram message delivery
  gittrack/        Git versioning for data directory
  eventlog/        JSONL event logging with daily rotation
  updater/         GHCR image update checker
```

## Security model

ALF runs as root inside the container to manage subprocess isolation. Claude runs as a restricted user (uid 1001):

- `/opt/alf/config.d/` - read-only for Claude
- `/opt/alf/tools.d/` - read + execute only
- `/home/alf/data/` - read + write (group-writable via umask)
- Secrets via Docker secrets mechanism, never in environment variables
- Security headers (HSTS, X-Frame-Options DENY, CSP, X-Content-Type-Options nosniff)
- Rate limiting (60 req/min global, 5 req/min on auth) + CORS on the Control Center API
- Auth via magic link (time-limited, rotating) with session cookies
- Session revocation - new magic link invalidates all previous sessions
- IP ban after repeated auth failures (configurable threshold and duration)

## Requirements

- **OS**: Linux or macOS (Docker required)
- **RAM**: 512 MB minimum (2 GB recommended for voice transcription)
- **Disk**: ~800 MB for the Docker image + ~600 MB on first voice message (Whisper model)
- **Network**: outbound HTTPS to Telegram API and Claude API

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, code conventions, and guidelines.

## License

[MIT](LICENSE)
