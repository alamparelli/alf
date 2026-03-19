---
category: Configuration
tags: codex, openai, gpt, setup, backend
order: 5
---

# OpenAI Codex

OpenAI Codex is an agentic coding tool that runs locally as a CLI. ALF can use it as an alternative backend alongside Claude, giving you access to GPT-5-codex for any tier.

Unlike API backends (OpenRouter, OpenAI), Codex is a **CLI provider** — ALF spawns `codex exec` subprocesses, similar to how it spawns `claude -p` for Claude tiers.

## Prerequisites

- Codex CLI is pre-installed in the ALF container (`codex-cli`)
- An OpenAI API key (from [platform.openai.com](https://platform.openai.com/api-keys))
- A ChatGPT Plus ($20/mo), Pro ($200/mo), or Business ($30/seat/mo) plan — or just an API key with credits

## Setup

### 1. Store your API key

Open the **Vault** tab in the Control Center, then:

1. Click **Add** in the Secrets section
2. Name: `codex_api_key`
3. Value: your OpenAI API key (starts with `sk-`)
4. Save

The daemon picks up the key automatically after vault unlock — no restart needed.

### 2. Configure a tier

In the **Tiers** tab, create or edit a tier:

- **Backend**: select `codex`
- **Model**: `gpt-5-codex` (default) or any model Codex supports
- Enable the tier and set a priority

Example tier config:

```json
{
  "name": "codex",
  "model": "gpt-5-codex",
  "backend": "codex",
  "priority": 2,
  "enabled": true,
  "routable": true,
  "router_label": "Codex for coding tasks"
}
```

### 3. Route messages to it

Configure the router to send coding-related messages to your Codex tier, or set it as a fallback tier.

## How it works

When a message is routed to a Codex tier, ALF:

1. Spawns `codex exec --json --full-auto --model <model> "<prompt>"`
2. Injects `CODEX_API_KEY` from the vault into the subprocess environment
3. Parses the JSONL event stream in real-time
4. Streams progress events (tool use, text output) to the chat UI

Codex runs in `--full-auto` mode with workspace-write sandbox, meaning it can read and edit files in the data directory without manual approval.

## Differences from Claude tiers

| Feature | Claude | Codex |
|---------|--------|-------|
| Tool control | Granular `--tools` whitelist | Sandbox-based (read-only or workspace-write) |
| Thinking events | Yes (visible in UI) | No |
| System prompts | `--system-prompt` flag | Prepended to user prompt |
| Effort level | `--effort low/medium/high` | Not supported |
| Write-capable toggle | Yes | Always workspace-write in full-auto |
| Cost tracking | Yes (from CLI metadata) | Token counts only |
| Session resume | `--resume <id>` | `codex exec resume <id>` |

## Authentication

Codex supports two auth methods. ALF uses **API key only**:

| Method | How | Used by ALF? |
|--------|-----|-------------|
| API key | `CODEX_API_KEY` env var | Yes — stored in vault |
| ChatGPT login | `codex login` → `~/.codex/auth.json` | No |

No `codex login` is needed. The API key from the vault is injected automatically.

## Setup Wizard

If you use the **Setup Wizard** during onboarding, select "OpenAI Codex" as a backend and enter your API key. The wizard stores it in the vault and makes the `codex` backend available in tier configuration.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "codex: skipped (no API key in vault)" | Add `codex_api_key` in Vault > Secrets |
| "start codex: exec: codex: not found" | Codex CLI not installed in container — update to latest image |
| "codex startup timeout" | API key may be invalid, or network issue reaching OpenAI |
| "codex: rate limit exceeded" | OpenAI rate limit — wait or upgrade plan |
| Codex not in backend dropdown | Key not in vault, or vault is locked |
