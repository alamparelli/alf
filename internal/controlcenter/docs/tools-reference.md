---
category: Reference
tags: tools, recall, remember, forget, schedule, react, status, extract-video, task, team, skill, app, config, tier, log, search
order: 20
---

# Tools Reference

ALF provides built-in CLI tools available in your PATH. These tools communicate with the daemon via native Go calls (API tiers) or CLI bridge (CLI tiers).

Both `~/data/tools.d/` (system tools) and `~/data/tools/` (user tools) are in PATH. All tools listed below can be called by name - no full path needed. User scripts placed in `~/data/tools/` with `chmod +x` are also callable by name.

## System Tools

These tools give you structured access to ALF's subsystems. On API tiers, they run as native Go tool calls (in-process, fast). On CLI tiers, they are available as CLI commands.

### task

Launch, list, cancel, or approve autonomous agent tasks. Tasks run in the background and can use teams for multi-agent orchestration.

```bash
task launch --prompt "Analyze the access logs and write a report"
task launch --prompt "Build the landing page" --team dev-team --skills app-builder
task launch --prompt "Review security" --need_validation
task list
task cancel <id>
task delete <id>
task approve <id> --approved true
task approve <id> --approved false --feedback "Split into smaller tasks"
```

| Flag | Required | Description |
|------|----------|-------------|
| `--prompt` | launch | The task objective |
| `--tier` | No | LLM tier for execution (default: agent tier) |
| `--team` | No | Team name for multi-agent execution |
| `--skills` | No | Comma-separated skill names to inject |
| `--need_validation` | No | Pause for user approval before executing |
| `--id` | cancel/delete/approve | Task ID |
| `--approved` | approve | `true` or `false` |
| `--feedback` | No | Message for the approval/rejection |

### team

Manage agent team configurations. Teams define groups of specialized agents for multi-agent task execution.

```bash
team list
team get ops-team
team save --name dev --description "Dev team" \
  --agents '[{"name":"coder","tier":"sonnet"},{"name":"reviewer","tier":"haiku"}]'
team delete old-team
```

| Flag | Required | Description |
|------|----------|-------------|
| `--name` | get/save/delete | Team name |
| `--description` | No | Team description (for save) |
| `--agents` | save | JSON array of `{name, tier, description?, skills?}` |

### skill

List available skills or get skill details. Skills provide specialized capabilities that auto-inject into conversations and tasks.

```bash
skill list
skill get tool-creator
```

### app

Manage installed apps and the marketplace.

```bash
app list                  # Installed apps with state
app catalog               # Browse remote marketplace
app install weather       # Install from marketplace
app update weather        # Update to latest version
app enable weather        # Activate app
app disable weather       # Deactivate app
app uninstall weather     # Remove app
```

### config

Read current system configuration (read-only).

```bash
config get
```

### tier

List available LLM tiers with models, backends, tools, and capabilities.

```bash
tier list
```

### log

Access daemon log files for debugging and monitoring.

```bash
log list                    # Available log files
log tail daemon.log         # Last 100 lines
log tail daemon.log 500     # Last 500 lines
```

### search

Search across apps, workspace files, and documentation.

```bash
search "weather"                     # Search everything
search "deploy" --types files        # Files only
search "oauth" --types apps,docs     # Apps and docs
```

### llm

Invoke a specific LLM tier for one-shot synchronous text processing.

```bash
llm <tier> "Classify this support ticket: ..."
llm <tier> "Summarize this document in 3 bullets: ..."
llm <tier> "Translate to French: Hello world" --system "You are a professional translator"
```

For async multi-step pipelines, use `agent_task chain` instead.

| Flag | Required | Description |
|------|----------|-------------|
| `tier` | Yes | LLM tier name (run `tier list` to see available tiers) |
| `prompt` | Yes | The prompt to send |
| `--system` | No | Optional system prompt for persona or constraints |

> **Legacy:** `llm` also supports `--fire-and-forget` with `--on-complete` for backward compatibility, but `agent_task chain` is preferred for its simpler flat schema.

### agent_task

Run background work: multi-agent team tasks or LLM chains.

#### Chain mode

Run a sequential LLM pipeline asynchronously. Returns immediately with a chain ID. The last step notifies the user (chat message + SSE + Telegram).

Use `{result}` in a step's prompt to inject the previous step's output (wrapped in `<chain_result status="200">...</chain_result>`).

```json
{
  "action": "chain",
  "steps": [
    {"tier": "haiku", "prompt": "Extract all TODOs from this code"},
    {"tier": "sonnet", "prompt": "Generate unit tests for these TODOs:\n{result}"}
  ]
}
```

3-step example:

```json
{
  "action": "chain",
  "steps": [
    {"tier": "haiku", "prompt": "List public functions in main.go"},
    {"tier": "sonnet", "prompt": "Write tests for:\n{result}"},
    {"tier": "haiku", "prompt": "Summarize what was tested:\n{result}"}
  ]
}
```

#### Launch mode

Launch a background team task with tool access for autonomous multi-step work.

```bash
agent_task launch --prompt "implement feature X" --tier sonnet --team content
```

| Flag | Required | Description |
|------|----------|-------------|
| `action` | Yes | `chain`, `launch`, `list`, `cancel`, `delete`, `approve` |
| `steps` | chain | Array of `{tier, prompt, system?}` objects (min 2) |
| `prompt` | launch | Task objective |
| `--tier` | No | LLM tier for launch |
| `--team` | No | Agent team name for launch |

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
  --command "df -h" --output chat

# LLM job
schedule create --name "morning brief" --schedule "0 0 9 * * 1-5" \
  --tier sonnet --prompt "Summarize today's priorities" --output chat

# One-shot (RFC3339 timestamp)
schedule create --name "deploy check" --schedule "2026-03-10T15:00:00Z" \
  --tier haiku --prompt "Check if v2.1 deployed correctly"
```

| Flag | Required | Description |
|------|----------|-------------|
| `--name` | Yes | Job name |
| `--schedule` | Yes | Cron expression (6 fields with seconds) or RFC3339 for one-shot |
| `--tier` | No | LLM tier name, or `direct` for bash. Auto-detected if `--command` given |
| `--prompt` | LLM jobs | Prompt sent to the LLM |
| `--command` | Direct jobs | Bash command to execute |
| `--output` | No | `chat` (TG+CC, default), `tg`, `cc`, `file`, `both` (chat+file), `silent` |
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

## Vault Tools

### vault proxy

Make authenticated API requests through the vault proxy. Credentials are injected server-side - ALF never sees the actual API keys.

```bash
vault proxy <service> <method> <path> [body]
vault proxy github GET /user
vault proxy slack POST /chat.postMessage '{"channel":"#general","text":"hello"}'
```

| Argument | Description |
|----------|-------------|
| `service` | Name of the vault service (registered in the Vault tab) |
| `method` | HTTP method (GET, POST, PUT, DELETE, PATCH) |
| `path` | API path (appended to the service's base URL) |
| `body` | Optional JSON body for POST/PUT/PATCH requests |

### vault list

List all registered vault services.

```bash
vault list
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

Requires: `ffmpeg`, `ffprobe`. Audio transcription uses the whisper-service container.
