---
category: Configuration
tags: config, settings, quiet hours, session, timezone, backends, tiers
order: 3
---

# Configuration Reference

All runtime settings live in `config.d/config.json`. Edit them via **Settings → Configuration** in the Control Center - changes take effect immediately without restarting.

## How to edit

**Option 1 - Settings tab (recommended).** Open **Settings** in the sidebar. The Configuration card shows all fields in a JSON editor with live validation.

**Option 2 - Workspace.** Go to **Home → Workspace**, navigate to `config.d/config.json`, and edit directly.

---

## General

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `log_level` | text | `"info"` | How much detail to show in the Logs tab. `"error"` = only problems, `"warn"` = problems + warnings, `"info"` = normal activity, `"debug"` = everything (very verbose). |
| `system_prompt` | text | `""` | Custom instructions added to every conversation. Use this to give ALF standing orders like "always reply in French" or "keep responses short". |
| `timezone` | text | `""` | Your timezone for schedules and quiet hours (e.g. `"Europe/Paris"`, `"America/New_York"`). Leave empty to use UTC. |

---

## Sessions

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `session_timeout` | number (minutes) | `30` | How long ALF waits before forgetting the current conversation. After this many minutes of silence, the next message starts fresh. Set to `0` to keep conversations alive forever (they never expire on their own). |
| `max_sessions` | number | `2` | How many separate conversations can be active at the same time. Each chat tab in the Control Center uses one session. `0` = use default (2). |

See [Managing Conversations](docs:sessions) for details on session lifecycle.

---

## Tiers

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tiers_file` | string | `"tiers.json"` | Filename (or absolute path) of the active tiers configuration. Relative paths are resolved inside `config.d/`. Change this to switch between tier profiles without modifying the default file. |
| `tiers_timeout` | int (seconds) | `300` | Maximum execution time for Claude tier invocations. `0` = use default (300 s). |

**Example - switching to an alternate tier profile:**
```json
{
  "tiers_file": "tiers-minimal.json"
}
```
The daemon reloads immediately and the file watcher follows the new path. To go back to the default, set `"tiers_file": "tiers.json"`.

See [Setting Up Tiers](docs:tier-setup) for tier configuration details.

---

## Quiet hours

Quiet hours prevent ALF from responding during a defined window. Useful for suppressing scheduled jobs or Telegram messages at night.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `quiet_hours.start` | int (hour, 0–23) | `0` | Start of the quiet window (inclusive). |
| `quiet_hours.end` | int (hour, 0–23) | `0` | End of the quiet window (exclusive). `0`/`0` = disabled. |

**Example - silence from 23:00 to 07:00:**
```json
{
  "quiet_hours": { "start": 23, "end": 7 }
}
```

---

## Security

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allowed_chat_ids` | int[] | `[]` | Telegram chat IDs allowed to interact. Empty = no restriction. |
| `auth_ban_threshold` | int | `10` | Failed `/auth` attempts before an IP is banned. `0` = use default (10). |
| `auth_ban_duration` | int (minutes) | `15` | Duration of an IP ban after threshold is reached. `0` = use default (15 min). |

---

## Auto-updates

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `auto_update_check` | bool | `true` | Periodically check for a new Docker image version. |
| `auto_update_check_interval` | int (seconds) | `21600` | How often to check (every 6 hours by default). `0` = use default. |
| `auto_update_notify` | bool | `true` | Send a Telegram notification when an update is available. |

---

## Git tracking

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `git_track` | bool | `true` | Enable automatic git commits of the `data/` directory. |
| `git_sweep_interval` | int (minutes) | `15` | How often to commit uncommitted changes. `0` = disabled. |

---

## Memory extraction

ALF automatically extracts facts from your activity into long-term memory. The extractor uses **git diff** on the data directory to capture both conversations and artifacts (documents, plans, contacts, skills).

### How it works

1. **Event-driven extraction**: triggered when a session ends (`/new`, `/clear`) or after a threshold of messages in a long session.
2. **Two-pass git diff**: ALF first shows the LLM a `git diff --stat` summary, then the LLM selects which files to examine in detail.
3. **Consolidation** (every 6h): deduplicates, merges, and cleans up the memory store. Also acts as a fallback for scheduled jobs that run without user interaction.

### Extraction guide

The file `context/extraction-guide.md` controls what the extractor looks for. ALF creates a default guide on first run. Edit it to match your needs — for example, a marketer might add "Always extract competitor names and pricing" while a developer might add "Focus on architecture decisions and dependency changes".

### Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `memory_enabled` | bool | `true` | Enable the memory system (embeddings + extraction). Set to `false` to disable entirely. |
| `memory_extract_timeout` | int (seconds) | `300` | Max time for each extraction LLM call. |
| `memory_extract_boot_delay` | int (seconds) | `180` | Delay before the first extraction after startup (avoids resource contention). |
| `memory_extract_min_messages` | int | `10` | Message threshold for mid-session extraction in long sessions. |
| `memory_dedup_text_threshold` | float | `0.7` | Jaccard word-similarity threshold for deduplication. Higher = stricter (fewer duplicates stored). Range: 0.0–1.0. |
| `memory_dedup_cosine_threshold` | float | `0.15` | Cosine distance threshold for semantic deduplication. Lower = stricter. Range: 0.0–2.0. |

### Memory types

| Type | Description |
|------|-------------|
| `fact` | Project details, stats, context worth remembering |
| `preference` | User likes/dislikes, style choices, workflow habits |
| `decision` | Choices made, approaches agreed upon |
| `contact` | People names, emails, companies, roles |

### Recall (auto-inject)

When you send a message, ALF automatically searches long-term memory for relevant context and injects matching memories into the conversation.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `recall_limit` | int | `5` | Max number of memories injected per message. |
| `recall_distance` | float | `1.0` | Max cosine distance for recall results. Lower = stricter (only very relevant). Range: 0.0–2.0. |

---

## DNS

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dns_servers` | string[] | `["8.8.8.8", "1.1.1.1"]` | DNS servers for the container. |

---

## Skills

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `show_skill_footer` | bool | `true` | Show active skill names at the bottom of each Telegram message. |

See [Creating Skills](docs:creating-skills) for skill setup.

---

## LLM Backends

`backends` is a map of named OpenAI-compatible API endpoints. Each key is a backend name used in tier configuration.

```json
{
  "backends": {
    "openrouter": {
      "base_url": "https://openrouter.ai/api/v1",
      "vault_service": "openrouter",
      "default_model": "anthropic/claude-haiku-4-5"
    },
    "ollama": {
      "base_url": "http://host.docker.internal:11434/v1",
      "auth": "none"
    }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `base_url` | string | API root URL (required). |
| `vault_service` | string | Vault service name holding the API key. Preferred over hardcoded keys. |
| `auth` | string | Auth scheme: `"bearer"` (default) or `"none"` (local Ollama). |
| `headers` | object | Extra HTTP headers (e.g. `HTTP-Referer` for OpenRouter). |
| `default_model` | string | Model to use when the tier doesn't specify one. |
| `max_tokens` | int | Max tokens per request. `0` = 4096. |

See [Backends & Models](docs:backends) for full setup instructions.

---

## What's next?

- [Setting Up Tiers](docs:tier-setup) - configure models, routing, and tier profiles
- [Backends & Models](docs:backends) - connect Ollama, OpenRouter, or any OpenAI-compatible API
- [Schedules](docs:schedules) - automate recurring tasks
- [Vault](docs:vault) - store API keys and secrets securely
