# OpenRouter Integration

OpenRouter provides access to all major LLM models (Claude, GPT, Gemini, Llama, Mistral, etc.) through a single API key. ALF can use OpenRouter as an alternative backend for both message routing and tier execution.

## Setup

1. Get an API key at [openrouter.ai/keys](https://openrouter.ai/keys)
2. Set the secret:
   ```
   alf secret set openrouter_api_key sk-or-v1-...
   ```
   Or configure during `alf init`.
3. Restart ALF: `docker compose down && docker compose up -d` (from the ALF directory)

## Configuring a Tier

In the Control Center Tiers tab, edit or create a tier:

- Set **Backend** to `openrouter`
- Set **Model** to any OpenRouter model ID (e.g. `google/gemini-2.0-flash`, `openai/gpt-4o`, `anthropic/claude-haiku-4-5`)

Or edit `tiers.json` directly:
```json
{
  "name": "gemini_r",
  "model": "google/gemini-2.0-flash",
  "backend": "openrouter",
  "priority": 2,
  "enabled": true,
  "routable": true,
  "router_label": "Fast responses using Gemini"
}
```

## Configuring the Router

The router classifies incoming messages and routes them to the right tier. By default it uses Claude CLI, but you can switch it to OpenRouter:

In `tiers.json`:
```json
{
  "router_backend": "openrouter",
  "router_model": "anthropic/claude-haiku-4-5",
  ...
}
```

Or use the Control Center: Tiers > Edit router settings > set Router backend.

## Model Discovery

Browse available models at [openrouter.ai/models](https://openrouter.ai/models). Use the full model ID (e.g. `anthropic/claude-sonnet-4`, not just `sonnet`).

## How It Works

- **CLI tiers** use Claude Code subprocess with `--resume` for conversation continuity
- **OpenRouter tiers** use HTTP API with a sliding-window message history (100 messages max, auto-expired after session timeout)
- The `/new` command clears both CLI sessions and API history
- If the OpenRouter API key is missing, tiers configured with `backend: "openrouter"` automatically fall back to CLI

## Limitations

- **Phase 1 is chat-only**: OpenRouter tiers cannot use tools (Read, Write, Bash, etc.). Use CLI tiers for agentic work.
- No streaming thinking/tool_use events — only text deltas are streamed
- Cost tracking is not available for OpenRouter calls (shows $0.00)

## Troubleshooting

| Issue | Solution |
|-------|----------|
| "OpenRouter API provider disabled" in logs | Set the `OPENROUTER_API_KEY` secret and restart |
| "api error 401" | Invalid API key — regenerate at openrouter.ai/keys |
| "api error 429" | Rate limited — ALF retries automatically (3x with backoff) |
| "api error 400: context too long" | ALF auto-truncates history and retries once |
| Model not found | Check the exact model ID at openrouter.ai/models |
| Tier falls back to CLI | The API key is missing or the backend field is not set to `openrouter` |
