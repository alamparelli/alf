---
category: Reference
tags: tools, recall, remember, forget, schedule, react, status, extract-video
order: 20
---

# Tools Reference

ALF provides built-in CLI tools available in your PATH. These tools communicate with the daemon via Unix sockets.

## Memory Tools

All memory tools connect to the memstore via `~/data/context/memstore.sock`.

### recall

Search long-term memory by semantic similarity.

```bash
recall "what does the user prefer for deployment?"
recall --limit 5 "project architecture"
recall --type preference "coding style"
recall --days 7 "recent decisions"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--limit N` | 5 | Max results to return |
| `--type T` | (all) | Filter by memory type (fact, preference, decision, etc.) |
| `--days N` | (all) | Only memories from the last N days |

### remember

Store a new memory.

```bash
remember "User prefers dark mode for all UIs"
remember --type preference "Always use bun instead of npm"
remember --type decision "Architecture uses event sourcing"
```

| Flag | Default | Description |
|------|---------|-------------|
| `--type T` | fact | Memory type: fact, preference, decision, instruction, context |

### forget

Delete a memory by ID (from `recall` output).

```bash
forget 42
```

## Scheduling Tools

Connects to the scheduler via `~/data/context/scheduler.sock`.

### schedule create

Create a scheduled job. Two modes: **direct** (bash command) or **LLM** (prompt sent to a tier).

```bash
# Direct bash job
schedule create --name "disk check" --schedule "0 0 */6 * * *" \
  --command "df -h" --output telegram

# LLM job
schedule create --name "morning brief" --schedule "0 0 9 * * 1-5" \
  --tier sonnet_r --prompt "Summarize today's priorities" --output telegram

# One-shot (RFC3339 timestamp)
schedule create --name "deploy check" --schedule "2026-03-10T15:00:00Z" \
  --tier haiku_r --prompt "Check if v2.1 deployed correctly"
```

| Flag | Required | Description |
|------|----------|-------------|
| `--name` | Yes | Job name |
| `--schedule` | Yes | Cron expression (6 fields with seconds) or RFC3339 for one-shot |
| `--tier` | No | LLM tier name, or `direct` for bash. Auto-detected if `--command` given |
| `--prompt` | LLM jobs | Prompt sent to the LLM |
| `--command` | Direct jobs | Bash command to execute |
| `--output` | No | `telegram` (default), `file`, `both`, `silent` |
| `--skills` | No | Comma-separated skill names to inject (LLM jobs only) |

### schedule list

```bash
schedule list          # All jobs
schedule list --user   # User jobs only (excludes system/managed)
```

### schedule update

```bash
schedule update <id> --enabled false
schedule update <id> --schedule "0 0 8 * * 1-5" --prompt "New prompt"
```

### schedule delete

```bash
schedule delete <id>
```

## Telegram Tools

These tools interact with the current Telegram conversation. They require the `ALF_SIGNAL_SOCK` environment variable (set automatically during Claude invocations).

### react

Add an emoji reaction to the user's message.

```bash
react "👍"
react "🔥"
```

### status

Update the typing status message shown to the user.

```bash
status "Analyzing code..."
status "Running tests..."
```

## Media Tools

### extract-video

Extract key frames and audio transcript from a video file. Outputs JSON.

```bash
extract-video /path/to/video.mp4
extract-video /path/to/video.mp4 --frames 10
extract-video /path/to/video.mp4 --no-audio
```

Output:
```json
{
  "frames": ["/tmp/frame-001.jpg", "/tmp/frame-002.jpg"],
  "duration_seconds": 15.3,
  "transcript": "Hello world...",
  "transcript_language": "en"
}
```

Requires: `ffmpeg`, `ffprobe`. Audio transcription uses faster-whisper (amd64) or whisper.cpp (arm64).
