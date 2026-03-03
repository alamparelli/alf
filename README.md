# ALF

A personal AI assistant that lives in your Telegram chats. Built in Go. Runs on your hardware.

ALF connects Claude to your messaging, wraps it with semantic long-term memory, configurable response tiers, voice transcription, and a web dashboard — all in a single Docker container you control.

## Why ALF

Most AI assistant frameworks are Node.js monoliths with hundreds of dependencies. ALF is different:

- **Go binary, zero JS runtime** — single static binary, minimal attack surface, no `node_modules` supply chain
- **Semantic memory** — Go-native ONNX embeddings (sqlite-vec + FTS5) for real long-term recall, not just context window tricks
- **Persistent classifier** — a long-lived Claude process handles message routing in ~0ms instead of spawning a new process per message
- **Tier system** — configurable response tiers (model, tools, effort, read/write access) routed by an LLM classifier
- **Defense-in-depth security** — Unix user isolation (uid 1001), read-only config, restricted tool execution, not just a container boundary
- **No API costs** — runs on your Claude subscription (Pro/Max/Team), not pay-per-token API calls
- **Self-hosted** — your hardware, your data, your rules. No cloud dependency beyond your Claude account

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
- A [Claude subscription](https://claude.ai/pricing) (Pro, Max, or Team — no API key needed)

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
4. Starting the container
5. Authenticating Claude

### After setup

```sh
alf login     # Authenticate Claude inside the container
alf status    # Check container health
alf logs      # Tail container logs
```

Send a message to your bot on Telegram. That's it.

## Features

### Semantic memory

ALF remembers things across conversations. The memory system uses sqlite-vec for vector similarity search and FTS5 for keyword matching, with Go-native ONNX Runtime inference (all-MiniLM-L6-v2) — no Python dependency for embeddings.

Claude has three memory tools:
- **recall** — hybrid semantic + keyword search over past memories
- **remember** — store facts, preferences, decisions, summaries
- **forget** — remove memories by ID

Memories are automatically recalled when relevant to the current conversation.

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

Send a voice message on Telegram. ALF transcribes it with faster-whisper and processes the text as a regular message. The Whisper model runs locally inside the container (auto-installed on first voice message).

### Media processing

- **Images** — forwarded to Claude's vision capabilities
- **Videos/GIFs** — frame extraction into contact sheets + audio transcription
- **PDFs** — text extraction via pdftotext
- **Documents** — passed through to Claude with appropriate context

### Control Center

Web dashboard at port 8080:

- **Chat** — web-based chat with SSE streaming
- **Workspace** — browse and edit configuration files, context, skills
- **Tiers** — configure response tiers in real-time
- **Status** — container health, model usage, session stats

Authentication via Telegram magic link (`/login` command) or bearer token.

### Daily mood

ALF has a rotating personality layer — 16 moods (sharp, philosophical, sardonic, playful, grumpy, zen, detective...) that cycle based on the date. This affects tone, not capability.

## CLI reference

```
alf init        Interactive setup wizard
alf start       Start the container
alf stop        Stop the container
alf restart     Restart the container
alf upgrade     Pull latest image and restart
alf login       Authenticate Claude inside the container
alf status      Show container status
alf logs        Tail container logs
alf secret      Manage secrets (list, set, remove)
alf uninstall   Remove ALF and its data
alf version     Print version
```

## Project structure

```
cmd/
  alf/             Host CLI (init, start, stop, upgrade, login)
  alf-daemon/      Container daemon (Telegram bot + Control Center + Claude management)
  extract-video/   System tool: video frame extraction + audio transcription
  memory-tools/    System tool: recall, remember, forget (semantic memory)

internal/
  cli/             CLI command implementations + embedded templates
  controlcenter/   HTTP server, auth, config CRUD, chat API, workspace
  provider/        Provider/Classifier interfaces + Claude CLI implementations
  router/          LLM-based message classification + tier routing
  memstore/        Semantic memory (SQLite + sqlite-vec + FTS5 + ONNX embedder)
  media/           Download, MIME detection, frame extraction, PDF parsing
  voice/           Persistent faster-whisper subprocess
  mood/            Daily mood rotation + live feedback
  session/         Claude session persistence (resume IDs)
  telegram/        Telegram Bot API client + Markdown→HTML
  memory/          System prompt assembly (soul, mood, context files)
  gittrack/        Git versioning for data directory
  eventlog/        JSONL event logging with daily rotation
  updater/         GHCR image update checker
```

## Security model

ALF runs as root inside the container to manage subprocess isolation. Claude runs as a restricted user (uid 1001):

- `/opt/alf/config/` — read-only for Claude
- `/opt/alf/tools/` — read + execute only
- `/home/node/data/` — read + write (group-writable via umask)
- Secrets via Docker secrets mechanism, never in environment variables
- Rate limiting (60 req/min) + CORS on the Control Center API
- Auth via magic link (time-limited) or bearer token with IP ban after repeated failures

## Requirements

- **OS**: Linux or macOS (Docker required)
- **RAM**: 512 MB minimum (2 GB recommended for voice transcription)
- **Disk**: ~800 MB for the Docker image + ~600 MB on first voice message (Whisper model)
- **Network**: outbound HTTPS to Telegram API and Claude API

## License

[MIT](LICENSE)
