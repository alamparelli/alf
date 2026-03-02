# ALF Architecture

> Keep this document updated on every structural change.
> Focus: systems, boundaries, and what is mutable vs immutable.

---

## System Overview

Two layers: a **host CLI** manages the lifecycle, a **container daemon** runs the bot.

```
Host machine                         Docker container (Debian node:22-slim)
┌──────────────┐                     ┌─────────────────────────────────────┐
│  alf CLI     │  docker compose     │  alf-daemon (PID 1, user: root)    │
│  cmd/alf/    │ ──────────────────► │  cmd/alf-daemon/                   │
│              │                     │                                     │
│  Manages:    │                     │  ┌──────────┐  ┌─────────────────┐ │
│  - init      │                     │  │ Telegram  │  │ Control Center  │ │
│  - start     │                     │  │ poller    │  │ :8080           │ │
│  - stop      │                     │  └─────┬────┘  └────────┬────────┘ │
│  - upgrade   │                     │        │                │          │
│  - login     │                     │        ▼                │          │
│  - secrets   │                     │  ┌──────────┐           │          │
│              │                     │  │ Claude    │  reloadCh │          │
│              │                     │  │ subprocess│◄──────────┘          │
│              │                     │  │ (uid:1001)│                      │
│              │                     │  └──────────┘                      │
│              │                     │  ┌──────────┐                      │
│              │                     │  │ Whisper   │                      │
│              │                     │  │ (python3) │                      │
│              │                     │  └──────────┘                      │
└──────────────┘                     └─────────────────────────────────────┘
```

---

## Privilege Separation (Two-User Model)

**Daemon runs as root** to enable `setuid` on Claude subprocesses.

| User | UID:GID | Runs | Purpose |
|------|---------|------|---------|
| `root` | 0:0 | alf-daemon, CC, whisper, background systems | Full access — writes config, spawns claude |
| `claude` | 1001:1000 (node group) | Claude CLI subprocess only | Restricted — writes to data via group, **no write to config** |

Claude is spawned via `SysProcAttr.Credential{Uid:1001, Gid:1000}` with `HOME=/home/node/data` — kernel-enforced, no shell wrapping.

---

## Packages

| Package | Role |
|---------|------|
| `cmd/alf/` | Host CLI router (init, start, stop, upgrade, login, secret, uninstall) |
| `cmd/alf-daemon/` | Container entry point — Telegram loop, command routing, Claude invocation |
| `cmd/extract-video/` | System tool — extract frames + audio transcript from videos |
| `internal/cli/` | All CLI command implementations + embedded templates |
| `internal/controlcenter/` | HTTP dashboard, auth, config/tiers/resource CRUD, stores, middleware |
| `internal/media/` | File download, MIME detection, frame extraction, audio extraction |
| `internal/voice/` | Persistent faster-whisper transcription subprocess |
| `internal/router/` | Message classification + tier routing |
| `internal/telegram/` | Markdown→HTML formatting, chunking, Telegram API client |
| `internal/session/` | Claude session persistence (--resume support) |
| `internal/mood/` | Reaction scoring, emoji sentiment, allowed reaction whitelist |
| `internal/memory/` | Memory index collection for system prompts |
| `internal/eventlog/` | JSONL event logger (daily rotation) |
| `internal/gittrack/` | Git version history for data directory |
| `internal/tierfs/` | Per-tier filesystem (system prompts, skills) |
| `internal/updater/` | GHCR tag polling for new image versions |

---

## Data Flow

### Text Message

```
Telegram message
  → daemon: getUpdates() long-poll
  → eventlog: log message_in
  → command routing (/login, /new, /help) OR:
  → router.Classify() → tier selection (runs as uid 1001)
  → askClaude(prompt, model, resumeID) (runs as uid 1001)
  → session store: persist session_id
  → telegram.SendMessage() → markdown→HTML → chunk → send
  → eventlog: log message_out
```

### Media Message (Photo/Document)

```
Telegram message (has photo/document)
  → download via media.DownloadFile()
  → detect MIME, save to /tmp
  → inject file path into prompt: [PHOTO — use Read tool: /tmp/alf-media-*.jpg]
  → bypass router → route to non-instant tier (needs Read tool)
  → askClaude(prompt) → Claude uses Read tool on file
  → cleanup: delete temp file after 10 minutes
```

### Video/GIF/Animation/VideoNote

```
Telegram message (has video/animation/video_note)
  → download via media.DownloadFile() → /tmp/alf-media-*.mp4
  → media.ExtractFrames() → ffmpeg → /tmp/alf-frame-*.jpg (3-5 frames)
  → media.ExtractAudio() → ffmpeg → /tmp/alf-audio-*.wav
  → transcriber.Transcribe() → faster-whisper → text + language
  → inject into prompt:
    [VIDEO (mp4, 15s) — 5 frames extracted. Use Read tool: /tmp/frame-001.jpg, ...]
    [Audio transcript: Hello world...]
  → bypass router → non-instant tier
  → askClaude(prompt) → Claude reads frame images
  → cleanup: delete video + frames + audio after 10 minutes
```

### Voice/Audio

```
Telegram message (has voice/audio)
  → transcriber.DownloadAndTranscribe()
  → faster-whisper → text + language
  → inject transcription as message text
  → normal text routing (router.Classify → askClaude)
```

---

## Volumes & Permissions

### Host directory (`~/alf/`)

```
alf/
├── docker-compose.yml          generated by alf init
├── secrets/                    mode 0700
│   ├── telegram_bot_token      mode 0600
│   ├── telegram_chat_id        mode 0600
│   └── cc_auth_token           mode 0600
├── config.d/                   → /opt/alf/config (rw for CC)
│   │                           → /home/node/data/config.d (ro for Claude)
│   ├── config.json             CC-managed config
│   ├── tiers.json              CC-managed tiers
│   └── router-prompt.md        Router classification prompt
├── data/                       → /home/node/data (rw)
│   ├── .claude/                CLI auth + session cache
│   ├── config/                 Claude's own config space (rw)
│   ├── config.d/               ro mount from host config.d/ (system config)
│   ├── tools.d/                per-tool symlinks → /opt/alf/tools/* (system tools)
│   ├── tools/                  Claude-created tools (rw)
│   ├── skills/                 Claude-created skills (rw)
│   ├── skills.d/               ro mount from host skills.d/ (user skills)
│   ├── logs/events/            JSONL per day
│   ├── sessions/               session persistence
│   ├── memories/               index.md + memory files
│   └── .git/                   gittrack repo (if enabled)
└── skills.d/                   → /home/node/data/skills.d (ro mount)
```

### Docker mounts (docker-compose.yml)

```yaml
volumes:
  - ./data:/home/node/data
  - ./config.d:/opt/alf/config          # CC reads/writes
  - ./config.d:/home/node/data/config.d:ro  # Claude reads only
  - ./skills.d:/home/node/data/skills.d:ro  # User skills (read-only)
```

**Note:** `tools.d` is NOT a host mount. The daemon creates per-tool symlinks at startup (e.g., `tools.d/extract-video → /opt/alf/tools/extract-video`). This avoids issues with host volume mounts overwriting image-time symlinks.

### Tools: two-tier model

| Path | Source | Access | Purpose |
|------|--------|--------|---------|
| `/opt/alf/tools/` | Docker image (baked at build) | rx (root:root, 755) | System tools: `extract-video`, etc. |
| `data/tools.d/*` | Per-tool symlinks → `/opt/alf/tools/*` | rx via symlink | Discovery path for Claude |
| `data/tools/` | Data volume | rw (root:node, g+ws) | Claude-created tools at runtime |

### Permission matrix

| Path | Owner | Mode | Daemon (root) | Claude (uid 1001, gid 1000) |
|------|-------|------|---------------|---------------------|
| `/opt/alf/config/` | root:root | 755 | read/write (CC) | **read-only** |
| `/opt/alf/tools/` | root:root | 755 | read-only | **read + execute** |
| `data/` | root:node | g+ws | read/write | read/write (via group) |
| `data/config/` | root:node | g+ws | read/write | read/write (Claude's own config) |
| `data/config.d/` | (ro mount) | — | read-only | **read-only** (system config) |
| `data/tools.d/*` | symlinks → /opt/alf/tools/* | — | read/execute | **read + execute** |
| `data/tools/` | root:node | g+ws | read/write | read/write |
| `data/skills/` | root:node | g+ws | read/write | read/write |
| `data/.claude/` | root:node | g+ws | read/write | read/write |
| `data/logs/` | root:node | g+ws | read/write | read/write |
| `data/sessions/` | root:node | g+ws | read/write | read/write |
| `data/skills.d/` | (ro mount) | — | read-only | **read-only** |

---

## Control Center

HTTP server on `:8080`, embedded in daemon process.

### Auth

Two mechanisms (either works):
1. **Token**: `CC_AUTH_TOKEN` in header/cookie — for API/automation
2. **Magic link**: Telegram `/login` → inline keyboard (24h/7d/30d) → one-time code → session cookie

### Routes

| Route | Auth | Method | Purpose |
|-------|------|--------|---------|
| `/health` | no | GET | Health check |
| `/auth?code=` | no | GET | Magic link consumption |
| `/` | yes | GET | Dashboard (embedded HTML/JS) |
| `/api/config` | yes | GET/POST | Config CRUD |
| `/api/tiers` | yes | GET/POST | Tier definitions |
| `/api/status` | yes | GET | Daemon stats |
| `/api/logs/{name}` | yes | GET | Tail log files |
| `/api/router-prompt` | yes | GET/PUT | Router prompt editor |
| `/api/memories/*` | yes | CRUD | Memory index files |
| `/api/{tools,skills}/*` | yes | CRUD | Resource management |
| `/api/restart` | yes | POST | Daemon restart signal |

### Logging

Successful GET requests to `/`, `/static/*`, `/favicon.ico`, and polling endpoints (`/api/logs`, `/api/status`, `/health`) are suppressed. Only errors and non-GET requests are logged.

### Hot reload

```
CC: POST /api/config → configStore.Save() → notifier.Notify(ReloadConfig) → reloadCh
daemon: select reloadCh → configStore.Load() → apply new config
```

Same pattern for tiers, tools, skills.

---

## Media Handling

### Supported Telegram types

| Type | Detection | Processing |
|------|-----------|------------|
| Photo | `msg.Photo != nil` | Download → save to /tmp → inject path in prompt |
| Document | `msg.Document != nil` | Download → detect MIME → text extract or path inject |
| Video | `msg.Video != nil` | Download → frame extraction + audio transcript |
| Animation (GIF) | `msg.Animation != nil` | Download → frame extraction (no audio) |
| VideoNote | `msg.VideoNote != nil` | Download → frame extraction + audio transcript |
| Voice | `msg.Voice != nil` | Download → whisper transcription → inject as text |
| Audio | `msg.Audio != nil` | Download → whisper transcription → inject as text |

### Frame extraction strategy

- Default: 5 frames evenly spaced across duration
- Short videos (<3s): 2 frames
- Very short (<1s) / GIFs: 1-3 frames
- Output: JPEG, max 1280px wide, quality 85
- Tool: `ffmpeg` with `select` filter for precise timestamps

### System tool: `extract-video`

Go CLI at `/opt/alf/tools/extract-video` (symlinked to `data/tools.d/`).
Claude can call it for videos it downloads itself (not just Telegram media).

```bash
extract-video /tmp/video.mp4 --frames 5
# → JSON: {"frames": [...], "duration_seconds": 15.3, "transcript": "...", "transcript_language": "en"}
```

---

## Daemon Startup Tasks

Run before any message processing begins:

1. **`fixClaudePermissions()`** — Sets `g+rw` on files and `g+rwx` on dirs inside `data/.claude/` so the claude subprocess (uid 1001) can read credentials and refresh OAuth tokens
2. **`linkSystemTools()`** — Creates per-tool symlinks in `data/tools.d/` pointing to `/opt/alf/tools/*` (host volume mount overwrites image-time symlinks)
3. **`migrateConfig()`** — Copies config files from old `data/config/` layout to `/opt/alf/config/`, cleans up orphan dirs (`tiers/`, `memory/`, `state/`)
4. **`memory.Bootstrap()`** — Seeds default memory files (soul.md, mood.md, index.md) if missing
5. **`mood.GenerateDaily()`** — Generates daily mood variation

---

## Background Systems

| System | Trigger | Configurable | Purpose |
|--------|---------|--------------|---------|
| **Telegram poller** | 30s long-poll loop | no | Core message ingestion |
| **Event logger** | every message in/out | no | JSONL audit trail, daily rotation |
| **Session store** | every Claude response | `session_timeout` (min) | `--resume` for conversation continuity |
| **Git tracker** | config/tier changes + periodic sweep | `git_track`, `git_sweep_interval` (min) | Version history for data/ |
| **Update checker** | periodic poll to GHCR | `auto_update_check`, `auto_update_check_interval` (sec), `auto_update_notify` | Notify on new image versions |
| **Magic link cleanup** | background goroutine | no | Expire unused codes |
| **Session cookie cleanup** | background goroutine | no | Expire old CC sessions |
| **Whisper transcriber** | started at daemon boot | `WHISPER_MODEL` env | Persistent python3 process, model loaded once |

---

## Config (`config.d/config.json`)

```json
{
  "log_level": "info",
  "allowed_chat_ids": [123456789],
  "system_prompt": "",
  "quiet_hours": {"start": 0, "end": 0},
  "session_timeout": 30,
  "git_track": true,
  "git_sweep_interval": 5,
  "auto_update_check": true,
  "auto_update_check_interval": 21600,
  "auto_update_notify": true
}
```

Mutable via Control Center dashboard or direct file edit. Changes detected via reload channel.

---

## Secrets

Docker Compose secrets mechanism. Daemon reads via `readSecret()` — checks `{VAR}_FILE` path first, falls back to env var.

| Secret | Required | Used by |
|--------|----------|---------|
| `TELEGRAM_BOT_TOKEN` | yes | Telegram API |
| `TELEGRAM_CHAT_ID` | yes | Default chat for notifications |
| `CC_AUTH_TOKEN` | no | Control Center auth |
| `ALLOWED_CHAT_IDS` | no | Login authorization (defaults to TELEGRAM_CHAT_ID) |

---

## Immutable vs Mutable

### Immutable (set at build/deploy time)
- Docker image (`ghcr.io/alamparelli/alf:latest`)
- Binary `/opt/alf/alf-daemon`
- System tools `/opt/alf/tools/*` (`extract-video`, etc.)
- Claude Code CLI (`npm install -g`)
- User accounts (root, node UID 1000, claude UID 1001)
- Container resource limits (2GB, 2 CPU)
- Port mapping

### Mutable by daemon/CC (at runtime)
- `/opt/alf/config/config.json` — via CC
- `/opt/alf/config/tiers.json` — via CC
- `/opt/alf/config/router-prompt.md` — via CC
- `data/logs/events/*.jsonl` — event logger
- `data/sessions/` — session store
- `data/.git/` — gittrack commits

### Mutable by Claude subprocess (via node group)
- `data/.claude/` — CLI auth + session cache (permissions fixed at startup by daemon)
- `data/config/` — Claude's own config space
- `data/tools/` — Claude-created tools
- `data/skills/` — Claude-created skills
- `data/memories/` — memory files

### Mutable by host user only
- `secrets/*` — via `alf secret set`
- `docker-compose.yml` — via `alf init` or manual edit
- `config.d/` — system config drop-ins
- `skills.d/` — user skill drop-ins
