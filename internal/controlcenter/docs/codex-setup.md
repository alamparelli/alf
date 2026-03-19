---
category: Configuration
tags: codex, openai, gpt, setup, backend
order: 5
---

# OpenAI Codex

OpenAI Codex is an agentic coding tool that runs locally as a CLI. ALF can use it as an alternative backend alongside Claude, giving you access to GPT-5-codex for any tier.

Unlike API backends (OpenRouter, OpenAI), Codex is a **CLI provider** — ALF spawns `codex exec` subprocesses, similar to how it spawns `claude -p` for Claude tiers.

## Prerequisites

- Codex CLI is pre-installed in the ALF container
- **Either** a ChatGPT subscription (Plus, Pro, Business) **or** an OpenAI API key with credits

## Authentication

ALF supports both Codex auth methods:

### Option A: ChatGPT subscription (codex login)

If you have a ChatGPT Plus ($20/mo), Pro ($200/mo), or Business plan:

1. Open the **Terminal** tab in the Control Center
2. Run `codex login --device-auth`
3. A one-time code is displayed — open the URL in your browser and enter the code
4. Once authenticated, Codex stores credentials in `~/.codex/auth.json`

No API key needed. ALF detects the login automatically.

This method gives you access to subscription credits and features like fast mode.

### Option B: API key

If you prefer using an OpenAI Platform API key:

1. Get a key from [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
2. Open the **Vault** tab in the Control Center
3. Click **Add** in the Secrets section
4. Name: `codex_api_key`, Value: your key (starts with `sk-`)
5. Save

The daemon picks up the key automatically — no restart needed.

API key usage is billed at standard OpenAI API rates (separate from ChatGPT subscription).

### Which to choose?

| | ChatGPT login | API key |
|---|---|---|
| **Billing** | ChatGPT subscription credits | Pay-per-use API rates |
| **Fast mode** | Available | Not available |
| **Setup** | `codex login` in Terminal | Store key in Vault |
| **Best for** | Users with ChatGPT subscription | CI/automation, pay-per-use |

## Setup

### 1. Authenticate (see above)

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
2. Uses `CODEX_API_KEY` from vault (if set) or `~/.codex/auth.json` (if logged in)
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

## Setup Wizard

If you use the **Setup Wizard** during onboarding, select "OpenAI Codex" as a backend. You can either enter an API key (stored in vault) or skip the key and use `codex login` afterwards in the Terminal tab.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "start codex: exec: codex: not found" | Codex CLI not installed — update to latest ALF image |
| "codex startup timeout" | Auth issue — run `codex login --device-auth` in Terminal, or check API key |
| "codex: rate limit exceeded" | OpenAI rate limit — wait or upgrade plan |
| Codex not in backend dropdown | Codex binary not found in container — update image |
| Auth expired | Run `codex login` again in Terminal to refresh tokens |
