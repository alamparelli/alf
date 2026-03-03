# ALF Architecture

> Keep this document updated on every structural change.
> Focus: systems, boundaries, and what is mutable vs immutable.

---

## System Overview

Two layers: a **host CLI** manages the lifecycle, a **container daemon** runs the bot.
A **mobile app** (React Native / Expo) connects to the daemon's HTTP API.

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
│  - login     │                     │        ▼                ▲          │
│  - secrets   │                     │  ┌──────────┐           │          │
│              │                     │  │ Claude    │  reloadCh │          │
│              │                     │  │ subprocess│◄──────────┘          │
│              │                     │  │ (uid:1001)│                      │
│              │                     │  └──────────┘                      │
│              │                     │  ┌──────────┐  ┌──────────┐       │
│              │                     │  │ Whisper   │  │ Chat API │       │
│              │                     │  │ (python3) │  │ (mobile) │       │
│              │                     │  └──────────┘  └──────────┘       │
└──────────────┘                     └─────────────────────────────────────┘
                                              ▲
Mobile app (React Native / Expo)              │
┌──────────────────────┐          HTTP / SSE  │
│  Chat, CC WebView,   │ ────────────────────►│
│  Settings            │          :8080       │
└──────────────────────┘
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
| `cmd/alf-daemon/` | Container entry point — Telegram loop, command routing, Claude invocation, chat API backend |
| `cmd/extract-video/` | System tool — extract frames + audio transcript from videos |
| `internal/cli/` | All CLI command implementations + embedded templates (`docker-compose.yml.tmpl`, `config.json.tmpl`) |
| `internal/controlcenter/` | HTTP dashboard, auth, config/tiers/workspace/chat CRUD, stores, middleware, notifier |
| `internal/media/` | File download, MIME detection, frame/audio extraction, PDF text extraction |
| `internal/voice/` | Persistent faster-whisper transcription subprocess |
| `internal/router/` | LLM-based message classification + tier routing, `ResolveModel()` for short→full model IDs |
| `internal/telegram/` | Markdown→HTML formatting, chunking, Telegram API client (send, react, edit, delete) |
| `internal/session/` | Claude session persistence (`--resume` support), backed by JSON file |
| `internal/mood/` | Daily mood generation, live feedback updater, reaction scoring, emoji sentiment, allowed reaction whitelist |
| `internal/memory/` | Memory index collection for system prompts, `Bootstrap()` seeds defaults |
| `internal/eventlog/` | JSONL event logger (daily rotation) |
| `internal/gittrack/` | Git version history for data directory |
| `internal/updater/` | GHCR tag polling for new image versions (OCI token auth) |

---

## Data Flow

### Text Message

```
Telegram message
  → daemon: getUpdates() long-poll
  → eventlog: log message_in
  → command routing (/login, /reset, /status) OR:
  → router.Classify() → tier selection or direct response (runs as uid 1001)
  → askClaude(prompt, model, resumeID) (runs as uid 1001)
    → injects: memory --context files, --append-system-prompt (reaction tag)
    → supports: --resume, --model, --allowedTools, --effort
  → session store: persist session_id
  → telegram.SendMessage() → markdown→HTML → chunk → send
  → reaction: mood.ShouldReact() → tg.SetMessageReaction()
  → eventlog: log message_out
```

### Media Message (Photo/Document)

```
Telegram message (has photo/document)
  → download via media.DownloadFile()
  → detect MIME, save to /tmp
  → PDF: pdftotext extraction → inject text content
  → other: inject file path into prompt: [PHOTO — use Read tool: /tmp/alf-media-*.jpg]
  → bypass router → route to non-instant tier (needs Read tool)
  → askClaude(prompt) → Claude uses Read tool on file
  → cleanup: delete temp file after 10 minutes
```

### Video/GIF/Animation/VideoNote

```
Telegram message (has video/animation/video_note)
  → download via media.DownloadFile() → /tmp/alf-media-*.mp4
  → media.ExtractFrames() → ffmpeg → contact sheet thumbnail grid
  → media.ExtractAudio() → ffmpeg → /tmp/alf-audio-*.wav
  → transcriber.Transcribe() → faster-whisper → text + language
  → GIF: emotion-specific prompt (reaction/meme context)
  → inject into prompt:
    [VIDEO (mp4, 15s) — contact sheet. Use Read tool: /tmp/contact-sheet-*.jpg]
    [Audio transcript: Hello world...]
  → bypass router → non-instant tier
  → askClaude(prompt) → Claude reads contact sheet image
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

### Reply Context

```
Telegram message (reply to another message)
  → extract ReplyToMessage metadata
  → format reply context: quoted text, sender, media type
  → inject into router classification and Claude prompt
  → enables contextual follow-up responses
```

### Chat API (Mobile App)

```
Mobile app → POST /api/chat (SSE stream)
  → ChatService.Send() → router.Classify() → askClaude()
  → SSE events: {type: "chunk", content: "..."} streamed to client
  → ChatStore persists message history (JSON file)
  → session tracking via apiChatID constant (isolated from Telegram sessions)

Mobile app → POST /api/chat/upload (multipart)
  → ChatMediaHandler validates file type/size
  → stores in data/chat-uploads/ with UUID filename
  → returns upload ID for embedding in messages

Mobile app → POST /api/chat/react
  → ChatReactHandler stores emoji reaction on message
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
├── skills.d/                   → /opt/alf/skills (rw)
│   │                           → /home/node/data/skills.d (ro for Claude)
│   └── ...                     user skill drop-ins
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
│   ├── chat-uploads/           mobile app uploaded media
│   └── .git/                   gittrack repo (if enabled)
└── ...
```

### Docker mounts (docker-compose.yml)

```yaml
volumes:
  - ./data:/home/node/data
  - ./config.d:/opt/alf/config          # CC reads/writes
  - ./config.d:/home/node/data/config.d:ro  # Claude reads only
  - ./skills.d:/opt/alf/skills          # Daemon reads/writes
  - ./skills.d:/home/node/data/skills.d:ro  # Claude reads only
```

Optional Traefik service (when `EnableHTTPS=true`) with Let's Encrypt ACME via HTTP challenge.

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
| `data/chat-uploads/` | root:node | g+ws | read/write | read/write |

---

## Control Center

HTTP server on `:8080`, embedded in daemon process.

### Auth

Two mechanisms (either works):
1. **Token**: `CC_AUTH_TOKEN` in header/cookie — for API/automation
2. **Magic link**: Telegram `/login` → inline keyboard (24h/7d/30d) → one-time code → session cookie

### Middleware stack

Outermost first: `loggingMiddleware` → `rateLimiter` (60 req/min per IP) → `corsMiddleware` → `authMiddleware` (Bearer token or session cookie) → `jsonMiddleware` (Content-Type on API responses)

### Routes

| Route | Auth | Method | Purpose |
|-------|------|--------|---------|
| `/health` | no | GET | Health check |
| `/auth?code=` | no | GET | Magic link consumption |
| `/` | yes | GET | Dashboard (embedded HTML/JS) |
| `/static/` | yes | GET | Embedded web assets (CSS, JS) |
| `/api/config` | yes | GET/PUT | Config CRUD |
| `/api/workspace` | yes | GET/PUT/DELETE | File browser over data/ and config.d/ |
| `/api/status` | yes | GET | Daemon stats (uptime, message count, version) |
| `/api/logs` | yes | GET | Tail event log files |
| `/api/memories/*` | yes | CRUD | Memory index files |
| `/api/tools/*` | yes | CRUD | Resource management, triggers ReloadTools |
| `/api/skills/*` | yes | CRUD | Resource management, triggers ReloadSkills |
| `/api/chat` | yes | POST/GET | POST: SSE stream chat; GET: message history |
| `/api/chat/upload` | yes | POST | Multipart media upload |
| `/api/chat/media/{id}` | yes | GET | Serve uploaded media by ID |
| `/api/chat/react` | yes | POST | Register emoji reaction on message |
| `/api/restart` | yes | POST | Send SIGTERM to PID 1 (container restart) |

### Logging

Successful GET requests to `/`, `/static/*`, `/favicon.ico`, and polling endpoints (`/api/logs`, `/api/status`, `/health`) are suppressed. Only errors and non-GET requests are logged.

### Hot reload

```
CC: POST /api/config → configStore.Save() → notifier.Notify(ReloadConfig) → reloadCh
daemon: select reloadCh → configStore.Load() → apply new config
```

Same pattern for tiers, tools, skills.

---

## Mobile App

React Native app (Expo ~52, React Native 0.76.9) in `mobile/`.

### Structure

```
mobile/
  App.tsx                     — root: bottom-tab navigator (Chat, CC, Settings)
  app.json                    — Expo config (bundle: com.alamparelli.alf)
  eas.json                    — EAS Build config (development + preview profiles)
  ios/                        — native iOS project (generated by expo run:ios)
  src/
    screens/
      ChatScreen.tsx          — main chat UI with SSE streaming
      CCScreen.tsx            — WebView embedding the CC dashboard
      SettingsScreen.tsx      — disconnect & reconfigure credentials
      SetupScreen.tsx         — first-run setup (server URL + auth token)
    components/
      ChatInput.tsx           — text + media attachment input bar
      MessageBubble.tsx       — message rendering with markdown
      ReactionPicker.tsx      — emoji reaction selector
      ReplyBar.tsx            — quoted-reply UI strip
    services/
      api.ts                  — CC API calls (sendMessage SSE, upload, react, history, status)
      auth.ts                 — SecureStore-backed server URL + token persistence
      media.ts                — image/document picker helpers
    theme.ts                  — design tokens
    types.ts                  — shared TypeScript types
```

### API integration

All requests go to `{serverUrl}/api/*` with `Authorization: Bearer {token}`.

| Endpoint | Usage |
|----------|-------|
| `POST /api/chat` | SSE stream — sends message, receives chunked response |
| `GET /api/chat?limit=&before=` | Paginated message history |
| `POST /api/chat/upload` | Multipart file upload → returns upload ID |
| `GET /api/chat/media/{id}` | Fetch uploaded media |
| `POST /api/chat/react` | Emoji reaction on message |
| `GET /api/status` | Daemon status for CC WebView |

### Key dependencies

`expo-image-picker`, `expo-document-picker`, `expo-av`, `expo-secure-store`, `react-native-markdown-display`, `react-native-webview`, `@react-navigation/bottom-tabs`

---

## Media Handling

### Supported Telegram types

| Type | Detection | Processing |
|------|-----------|------------|
| Photo | `msg.Photo != nil` | Download → save to /tmp → inject path in prompt |
| Document | `msg.Document != nil` | Download → detect MIME → PDF text extract or path inject |
| Video | `msg.Video != nil` | Download → contact sheet extraction + audio transcript |
| Animation (GIF) | `msg.Animation != nil` | Download → contact sheet (no audio), emotion-specific prompt |
| VideoNote | `msg.VideoNote != nil` | Download → contact sheet extraction + audio transcript |
| Voice | `msg.Voice != nil` | Download → whisper transcription → inject as text |
| Audio | `msg.Audio != nil` | Download → whisper transcription → inject as text |

### Frame extraction strategy

- Default: 5 frames evenly spaced across duration → assembled into contact sheet grid
- Short videos (<3s): 2 frames
- Very short (<1s) / GIFs: 1-3 frames
- Output: JPEG contact sheet thumbnail, max 1280px wide, quality 85
- Tool: `ffmpeg` with `select` filter for precise timestamps

### System tool: `extract-video`

Go CLI at `/opt/alf/tools/extract-video` (symlinked to `data/tools.d/`).
Claude can call it for videos it downloads itself (not just Telegram media).

```bash
extract-video /tmp/video.mp4 --frames 5
# → JSON: {"frames": [...], "duration_seconds": 15.3, "transcript": "...", "transcript_language": "en"}
```

---

## Daemon Startup Sequence

Run before any message processing begins:

1. **`syscall.Umask(0o002)`** — Ensures all created files are group-writable
2. **Read secrets** — `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`, `CC_AUTH_TOKEN`
3. **Verify `claude` CLI** — checks PATH availability
4. **Resolve directories** — `dataDir`, `configDir`, `skillsDir` (env overrides available)
5. **Parse `allowedChatIDs`** — defaults to `TELEGRAM_CHAT_ID`
6. **Init shared state** — `Stats`, `reloadCh`, `MagicStore` (with cleanup goroutine), `FileSessionStore` (with cleanup goroutine)
7. **Write `.version` file** — stamps current version in data dir
8. **`os.MkdirAll`** — creates `logs/events/`, `sessions/`, `config/`, `tools/`, `skills/`, `memories/`
9. **`linkSystemTools()`** — symlinks `/opt/alf/tools/*` into `data/tools.d/`
10. **`fixDataPermissions()`** — chmod/chown correction for pre-refactor files
11. **`migrateConfig()`** — moves config from old `data/config/` layout to `configDir`
12. **Load stores** — `FileConfigStore` + `FileTierStore`
13. **`memory.Bootstrap()`** — seeds `soul.md`, `mood.md`, `index.md` if missing
14. **`mood.GenerateDaily()`** — overwrites `mood.md` if date changed
15. **Init `session.Store`** — Claude `--resume` tracking (file-backed)
16. **Init `eventlog.Logger`** — JSONL, daily rotation
17. **Init `gittrack.Tracker`** — `git init`, optional `StartSweep()` goroutine (if `cfg.GitTrack`)
18. **Start `voice.Transcriber`** — persistent faster-whisper Python process
19. **Create `ChatStore` + `ChatService`** — mobile app chat API backend
20. **Start CC HTTP server** — `:8080` as goroutine
21. **Init `updater.Checker`** — polls GHCR every 6h (if `cfg.AutoUpdateCheck`)
22. **Enter Telegram polling loop**

---

## Background Systems

| System | Trigger | Configurable | Purpose |
|--------|---------|--------------|---------|
| **Telegram poller** | 35s long-poll loop | no | Core message ingestion |
| **Event logger** | every message in/out | no | JSONL audit trail, daily rotation |
| **Session store** | every Claude response | `session_timeout` (min) | `--resume` for conversation continuity |
| **Git tracker** | config/tier changes + periodic sweep | `git_track`, `git_sweep_interval` (min) | Version history for data/ |
| **Update checker** | periodic poll to GHCR | `auto_update_check`, `auto_update_check_interval` (sec), `auto_update_notify` | Notify on new image versions |
| **Magic link cleanup** | background goroutine | no | Expire unused codes |
| **Session cookie cleanup** | background goroutine | no | Expire old CC sessions |
| **Whisper transcriber** | started at daemon boot | `WHISPER_MODEL` env | Persistent python3 process, model loaded once |
| **Chat service** | HTTP requests from mobile app | no | SSE streaming, message persistence, media uploads |

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

## Docker Image

**Builder:** `golang:1.24-alpine` — builds `alf-daemon` + `extract-video`

**Runtime:** `node:22-slim` — system packages: `bash`, `ca-certificates`, `curl`, `ffmpeg`, `git`, `trash-cli`, `python3`, `python3-pip`, `libgomp1`, `poppler-utils`

**Python:** `faster-whisper` (pip3)

**Go:** `go1.24.1` available at runtime for Claude to build tools

**Node:** `@anthropic-ai/claude-code` (global npm install)

**ENV:** `OMP_NUM_THREADS=4`, PATH includes `/opt/alf/tools` and Go bin dirs

**Exposed port:** `8080`

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
- `data/chat-uploads/` — mobile app media
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
