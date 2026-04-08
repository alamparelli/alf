---
category: Reference
tags: system tools, task, team, skill, app, config, tier, log, search, native tools, API, CLI
order: 21
---

# System Tools

System tools give ALF structured access to its own subsystems. Instead of relying on instructions or improvising, the LLM calls dedicated tools with typed parameters and gets structured results.

## How they work

System tools run on both API and CLI tiers through different execution paths:

| Tier type | Execution path | Speed |
|-----------|---------------|-------|
| **API** (OpenRouter, etc.) | Native Go — in-process, no HTTP | Fast |
| **CLI** (Claude CLI) | Bridge binary — HTTP call to daemon | Slightly slower |

The LLM doesn't need to know which path is used. The tools appear the same in both contexts.

### API tiers

Tools are registered as native Go implementations. When the API model returns a `tool_call`, the `ToolLoop` executes them in-process via `Executor.Execute()`. No subprocess, no network — direct function call.

The tool schemas are converted to OpenAI function calling format via `ToOpenAI()` and sent alongside the prompt.

### CLI tiers

Tools are listed in `toolbox.md` (auto-generated at boot) with their descriptions and usage patterns. The LLM calls them via `bash`. Each tool name is a symlink in `tools.d/` pointing to the `system-tools` multi-call binary, which sends HTTP requests to the daemon's Control Center API.

## Available tools

### task — Agent orchestration

Launch autonomous tasks that run in the background. Tasks can use teams for multi-agent workflows and support validation gates.

**Actions:** `launch`, `list`, `cancel`, `delete`, `approve`

```bash
# Launch a simple task
task launch --prompt "Analyze access logs from the last 24h"

# Launch with a team and skills
task launch --prompt "Build the new landing page" \
  --team dev-team --skills app-builder,tool-creator

# Launch with validation (pauses for approval before executing)
task launch --prompt "Refactor the auth module" --need_validation

# Monitor
task list

# Control
task cancel abc123
task approve abc123 --approved true
task approve abc123 --approved false --feedback "Too broad, split into smaller tasks"
```

**Key parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `prompt` | string | Task objective (required for launch) |
| `tier` | string | LLM tier override (default: `agent` tier) |
| `team` | string | Team name for multi-agent execution |
| `skills` | string | Comma-separated skill names |
| `need_validation` | boolean | Require user approval of the plan |
| `id` | string | Task ID (for cancel/delete/approve) |
| `approved` | boolean | Approval decision |
| `feedback` | string | Reason for approval/rejection |

### team — Agent team management

Create, list, and manage agent teams. Each team defines a group of specialized agents with their own tiers, skills, and roles.

**Actions:** `list`, `get`, `save`, `delete`

```bash
# List all teams
team list

# Get team details
team get ops-team

# Create a team
team save --name "content-team" --description "Content creation pipeline" \
  --agents '[
    {"name": "researcher", "tier": "sonnet", "description": "Researches topics"},
    {"name": "writer", "tier": "sonnet", "description": "Writes content"},
    {"name": "editor", "tier": "haiku", "description": "Reviews and edits"}
  ]'

# Delete a team
team delete old-team
```

**Agent fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Agent identifier |
| `tier` | Yes | LLM tier (defines model, tools, turns) |
| `description` | No | What this agent does |
| `skills` | No | Skill names to inject into this agent |

### skill — Skill catalog

Browse and inspect available skills. Skills auto-inject into conversations when their trigger keywords match.

**Actions:** `list`, `get`

```bash
# List all skills (system + user)
skill list

# Get skill details including full prompt
skill get tool-creator
```

Each skill shows:
- `name` — skill identifier
- `description` — what it does
- `triggers` — keywords that activate it
- `tier` — minimum tier required
- `source` — `system` or `user`

### app — App & marketplace management

Manage installed apps and browse the marketplace catalog.

**Actions:** `list`, `catalog`, `install`, `update`, `enable`, `disable`, `uninstall`

```bash
app list          # Installed apps with enabled/disabled state
app catalog       # Browse remote marketplace
app install weather
app update weather
app enable weather
app disable weather
app uninstall weather
```

### config — System configuration

Read the current system configuration (read-only). Sensitive fields like API keys and vault services are redacted.

**Actions:** `get`

```bash
config get
```

Returns: log level, quiet hours, session timeout, git tracking settings, timezone, backends, memory settings, etc.

### tier — LLM tier listing

List all configured LLM tiers with their capabilities.

**Actions:** `list`

```bash
tier list
```

Each tier shows:
- `name` — tier identifier (user-defined, from tier configuration)
- `model` — LLM model name
- `backend` — execution backend (cli, openrouter, etc.)
- `enabled` / `routable` — availability flags
- `tools` — available tool names
- `effort` — thinking effort level

### log — System log access

Read daemon logs for debugging and monitoring.

**Actions:** `list`, `tail`

```bash
log list                    # Available log files
log tail daemon.log         # Last 100 lines (default)
log tail daemon.log 500     # Last 500 lines (max)
```

### search — Cross-resource search

Search across installed apps, workspace files, and documentation.

```bash
search "oauth"                        # Search all types
search "deploy" --types files         # Files only
search "weather" --types apps,docs    # Apps and docs only
```

**Type filters:** `apps`, `files`, `docs` (comma-separated, default: all)

### avatar — Profile image

Change the LLM's profile avatar displayed in chat messages. Images are sanitized (decode, resize to 128x128, re-encode as PNG).

**Actions:** `set`, `reset`, `status`

```bash
avatar set <base64_image>    # Upload new avatar (PNG/JPEG/WebP, max 256KB)
avatar reset                 # Remove custom avatar
avatar status                # Check if custom avatar is set
```

See [Avatar](docs:avatar) for security details and API reference.

## Creating user tools

System tools are built-in and cannot be modified. To create your own tools, place executable scripts in `~/data/tools/` and optionally add a `.json` schema file alongside.

See [Creating Skills](docs:creating-skills) for the skill-based approach, or ask ALF to help you build a tool using the `tool-creator` skill.

## Architecture

```
┌──────────────────────────────────────────┐
│              LLM (any tier)              │
├─────────────────┬────────────────────────┤
│   API tier      │      CLI tier          │
│                 │                        │
│  tool_call      │  bash "task launch .." │
│      │          │         │              │
│  ToolLoop       │  system-tools binary   │
│      │          │         │              │
│  Executor       │  HTTP → CC API         │
│      │          │         │              │
│  NativeTool     │  Same handlers         │
│   .Run()        │                        │
├─────────────────┴────────────────────────┤
│          Subsystem adapters              │
│  (Orchestrator, Teams, Skills, etc.)     │
└──────────────────────────────────────────┘
```

## What's next

- [Agent Teams](docs:agent-teams) — configure multi-agent teams
- [Creating Skills](docs:creating-skills) — teach ALF new abilities
- [Tools Reference](docs:tools-reference) — full CLI tools reference
