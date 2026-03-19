---
category: Configuration
tags: backends, openrouter, ollama, openai, api, models
order: 4
---

# LLM Backends

ALF can use different AI providers beyond Claude. You can connect services like OpenRouter (access to 200+ models), OpenAI (GPT-4), Ollama (free local models), or OpenAI Codex (agentic coding CLI) - and use them alongside Claude.

## Quick Setup

Add backends to the `backends` section in `config.json`:

```json
{
  "backends": {
    "openrouter": {
      "base_url": "https://openrouter.ai/api/v1"
    },
    "openai": {
      "base_url": "https://api.openai.com/v1"
    }
  }
}
```

Then store the API key in the vault:

1. Open **Vault** tab in the Control Center
2. In the **Secrets** section, click **Add**
3. Name: `openrouter_api_key`, Value: your API key
4. Repeat for other backends (e.g. `openai_api_key`)

The daemon loads API keys from the vault automatically — no restart needed after vault unlock.

## Built-in Presets

| Backend | Base URL | Auth | Notes |
|---------|----------|------|-------|
| OpenRouter | `https://openrouter.ai/api/v1` | bearer | Access to 200+ models via single key |
| OpenAI | `https://api.openai.com/v1` | bearer | GPT-4o, o1, o3, etc. |
| Ollama | `http://host.docker.internal:11434/v1` | none | Local models, no API key needed |
| Codex | — (CLI provider) | API key | GPT-5-codex via `codex exec` — see docs:codex-setup |

## Backend Config Fields

```json
{
  "base_url": "https://api.example.com/v1",   // required
  "auth": "bearer",                            // "bearer" (default) or "none"
  "vault_service": "my-service",               // vault service name for API key
  "headers": {"HTTP-Referer": "https://alf"},  // custom headers
  "default_model": "gpt-4o",                   // fallback if tier doesn't specify
  "max_tokens": 4096                           // 0 = 4096
}
```

## Authentication

API keys are stored in the vault with the naming convention `<backend_name>_api_key`:

- `openrouter_api_key` → OpenRouter
- `openai_api_key` → OpenAI
- `codex_api_key` → OpenAI Codex
- `custom_name_api_key` → custom backend named "custom_name"

Add keys via the **Vault** tab → **Secrets** → **Add** in the Control Center.

The daemon loads API keys from the vault on startup and after vault unlock. When the vault is locked, backends without keys are skipped.

Backends with `"auth": "none"` (e.g. Ollama) skip authentication entirely.

## Model Discovery

When you select a backend in the Tiers configuration, ALF automatically fetches the list of available models from the provider's API:

- **Ollama**: queries `GET /api/tags` for locally installed models
- **OpenAI**: queries `GET /v1/models` for available models
- **OpenRouter**: queries `GET /api/v1/models` for the full model catalog

The model dropdown is populated dynamically - no need to type model IDs manually. Results are cached per session.

You can also query models programmatically:
```
GET /api/backends/{name}/models
```

Returns `{"backend": "ollama", "models": [{"id": "llama3.2:latest"}, ...]}`.

## Configuring a Tier

In the Control Center Tiers tab, set the **Backend** dropdown to your backend name. The model dropdown will update automatically with available models:

```json
{
  "name": "fast",
  "model": "google/gemini-2.0-flash",
  "backend": "openrouter",
  "priority": 1,
  "enabled": true,
  "routable": true,
  "router_label": "Fast responses"
}
```

## Configuring the Router

The router classifies messages and routes them to tiers. Switch it to an API backend in `tiers.json`:

```json
{
  "router_backend": "openrouter",
  "router_model": "anthropic/claude-haiku-4-5"
}
```

Or use the Control Center: Tiers > Router settings.

## Cross-backend context

Conversation history flows seamlessly across backends. When the router switches a message from a CLI tier to an API tier (or vice versa), the unified conversation store provides context to the new provider:

- **API backends** receive conversation history as structured messages in the API request
- **CLI backends** receive conversation history as an injected system prompt (when no active `--resume` session exists)

This means users don't lose context when the router switches tiers mid-conversation.

## Good to know

- API models can use ALF's built-in tools (reading files, running commands, etc.), but some advanced features only work with Claude. Use Claude tiers for complex multi-step work.
- Responses from API models stream text only - you won't see the "thinking" or "using tool" indicators that Claude shows
- Cost tracking isn't available for API models (always shows $0.00)
- When setting up tools for an API tier, only compatible tools are shown in the list

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "backend X: skipped (no API key in vault)" | Add the key in Vault → Secrets → Add (`<name>_api_key`) |
| API error 401 | Invalid API key - regenerate it |
| API error 429 | Rate limited - ALF retries automatically (3x with backoff) |
| API error 400: context too long | ALF auto-truncates history and retries once |
| Tier falls back to CLI | Backend not configured or API key missing |
| Backend not in dropdown | Add it to `config.json` backends and restart |
