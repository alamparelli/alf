# Setup Wizard

The Setup Wizard provides a guided onboarding experience in the Control Center, replacing the CLI-only `alf init` workflow. It enables users to configure ALF entirely from the web UI — a requirement for multi-tenant deployments where clients don't have SSH access.

## Wizard Flow

```
[1] Backend LLM → [2] Claude Auth → [3] Telegram → [4] Tier Preset → [5] Done
```

- Step 2 is shown only if Claude CLI is selected as a backend
- Step 3 is optional (skip creates a CC-only instance)
- Step 4 loads presets from `config.d/setup-presets/*.json`

## API Endpoints

### Setup Status

```
GET /api/setup/status
```

Returns which setup steps are completed:

```json
{
  "steps": {
    "backend": true,
    "claude_auth": false,
    "telegram": true,
    "tiers": true
  },
  "completed": false
}
```

Detection logic:
- `backend`: at least one API backend configured OR Claude CLI authenticated
- `claude_auth`: `$HOME/.claude.json` exists (file size > 2 bytes)
- `telegram`: bot token AND chat ID present (vault or Docker secrets)
- `tiers`: at least one enabled tier in current tiers config
- `completed`: `backend && tiers` (telegram is optional)

### Presets

```
GET /api/setup/presets
```

Reads tier presets from `config.d/setup-presets/` directory. Each `.json` file is one preset.

Response:
```json
{
  "presets": {
    "claude": [ { "id": "claude-default", "name": "...", "tiers": [...] } ],
    "openrouter": [ ... ]
  }
}
```

### Backend Test

```
POST /api/setup/backend/test
Content-Type: application/json

{ "type": "openrouter", "base_url": "https://openrouter.ai/api/v1", "api_key": "sk-or-..." }
```

Tests connectivity to a backend. Returns `{ "ok": true }` or `{ "ok": false, "error": "..." }`.

### Telegram Validation

```
POST /api/setup/telegram/validate
Content-Type: application/json

{ "bot_token": "123456789:ABCdef..." }
```

Validates bot token via Telegram API. Returns `{ "ok": true, "bot_name": "my_bot" }`.

### Claude Auth Check

```
GET /api/setup/claude/check
```

Returns `{ "authenticated": true/false }`.

### Ollama Model Discovery

```
GET /api/setup/ollama/models?base_url=http://host.docker.internal:11434
```

Lists models installed on an Ollama instance. Returns `{ "models": ["llama3", "codellama", ...] }`.

### Apply Setup

```
POST /api/setup/apply
Content-Type: application/json

{
  "backends": {
    "openrouter": { "base_url": "...", "api_key": "sk-or-..." }
  },
  "telegram": { "bot_token": "...", "chat_id": "..." },
  "preset_id": "claude-default",
  "timezone": "Europe/Brussels"
}
```

Applies the full wizard configuration:
1. Writes API keys to vault (or secret files if vault is locked)
2. Updates `config.json` with backends and timezone
3. Writes `tiers.json` from selected preset
4. Stores Telegram credentials if provided
5. Triggers config and tier hot-reload
6. Returns `{ "ok": true, "restart_required": true/false }`

## Preset File Format

Place preset files in `config.d/setup-presets/`. Each file is a JSON object:

```json
{
  "id": "claude-default",
  "name": "Claude Default",
  "description": "Full Claude stack with auto-routing",
  "backend": "claude",
  "router_config": {
    "router_model": "haiku",
    "router_backend": "",
    "default_fallback": "haiku",
    "router_distinctions": "Pick the tier that best balances capability and cost..."
  },
  "tiers": [
    {
      "name": "haiku",
      "model": "haiku",
      "priority": 1,
      "enabled": true,
      "routable": true,
      "router_label": "Casual conversation, simple tasks...",
      "max_turns": 30,
      "write_capable": true,
      "effort": "low",
      "force_command": true
    }
  ]
}
```

Fields:
- `id`: unique identifier (must match filename without extension)
- `name`: display name shown in wizard
- `description`: short description of the preset
- `backend`: which backend type this preset is for (`claude`, `openrouter`, `openai`, `ollama`)
- `router_config`: router settings applied when this preset is selected
- `tiers`: array of tier configurations (same format as `tiers.json`)

## Re-running the Wizard

The wizard can be re-run from Settings. When re-run, fields are pre-filled with current configuration values.
