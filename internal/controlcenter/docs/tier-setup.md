---
category: Configuration
tags: tiers, routing, models, haiku, sonnet, opus
order: 2
---

# Setting Up Tiers

Customize how ALF picks the right AI model for each message.

## What are tiers?

Think of tiers like different assistants sitting at your desk:

- **The quick one** answers "hey" and "thanks" instantly. Cheap, fast.
- **The smart one** writes emails, reviews code, explains things. A good all-rounder.
- **The expert** tackles architecture redesigns and complex research. Powerful, but costs more.

Every time you send a message, ALF's **router** reads it and picks the best tier. You control the rules.

## Where to edit tiers

**Option 1 - Tiers tab (recommended).** Open the Control Center sidebar and click **Tiers**. You'll see all tiers in a visual list. Click **Add Tier** or **Edit** to use the form with dropdowns, checkboxes, and tool selection.

**Option 2 - Workspace editor.** Go to **Home > Workspace**, navigate to `config.d/tiers.json`, and edit the JSON directly.

Changes take effect immediately - no restart needed.

## The default setup

ALF comes with 5 tiers out of the box. Here's what each one does:

| Tier | Model | What it handles | Can edit files? |
|------|-------|----------------|:-:|
| `instant` | Haiku | "Hi", "thanks", "ok", yes/no | No |
| `haiku` | Haiku | Chat, Q&A, summaries, translations, quick edits | Yes (max 5 steps) |
| `sonnet` | Sonnet | Code review, analysis, research, write code, fix bugs | Yes (max 10 steps) |
| `opus` | Opus | Architecture, deep analysis, large refactoring | Yes (max 20 steps) |
| `agent` | Opus | Multi-agent coordination for complex tasks | Via agents |

The agent tier is **disabled by default**. Enable it in `tiers.json` to let the router automatically delegate complex tasks to agent teams. See [Agent Teams](docs:agent-teams) for setup.

> Most of your messages will be handled by Haiku - it's fast and cheap. Sonnet and Opus only kick in when needed.

## How the router decides

The router follows these rules:

1. **Simple question?** Use Haiku. Chat, facts, jokes, translations - all Haiku.
2. **Needs thinking?** Use Sonnet. Code review, analysis, writing structured content.
3. **Really complex?** Use Opus. Only for system-wide architecture or holding many constraints at once.
4. **Need to change files?** Each tier is write-capable. The router picks the right model based on complexity.

> If the router can't decide, it falls back to `haiku`. Fast and safe.

## Understanding the config file

Here's what a `tiers.json` file looks like:

```json
{
  "router_model": "haiku",
  "default_fallback": "haiku",
  "router_distinctions": "Use haiku for casual chat. Use sonnet for analysis and code. Use opus only for complex architecture.",
  "tiers": [
    {
      "name": "base",
      "model": "haiku",
      "priority": 1,
      "enabled": true,
      "routable": true,
      "router_label": "Casual chat, simple questions, quick answers",
      "effort": "low"
    }
  ]
}
```

### Top-level settings

| Setting | What it does | Example |
|---------|-------------|---------|
| `router_model` | Which model classifies your messages. Keep this cheap and fast. | `"haiku"` |
| `default_fallback` | Which tier to use when the router can't decide. | `"haiku"` |
| `router_distinctions` | Plain English rules for the router. This is your main control lever. | `"Use haiku for simple tasks..."` |

### Per-tier settings

| Setting | What it does | Example |
|---------|-------------|---------|
| `name` | Unique name. Shows up in logs and status messages. | `"sonnet"` |
| `model` | Which Claude model to use: `haiku`, `sonnet`, or `opus`. | `"sonnet"` |
| `priority` | Ranking order (1 = first choice, 2 = second, etc.). When ALF can't decide, it picks the lowest number. | `3` |
| `enabled` | Set to `false` to turn off a tier completely. | `true` |
| `routable` | Set to `false` to hide from the router (manual-only). | `true` |
| `instant` | Reply instantly without starting a full AI session. Best for simple greetings like "hi" or "thanks". | `false` |
| `router_label` | Describes what this tier is good at. The router reads this to decide. **This is the most important field.** | `"Code review, debugging"` |
| `description` | Optional longer description. Falls back to `router_label` if not set. Used in router prompt. | `"Deep code analysis"` |
| `write_capable` | Can this tier create, edit, or delete files? | `false` |
| `effort` | How hard the model thinks: `low`, `medium`, or `high`. **CLI tiers only** - this maps to Claude's `--effort` flag. Ignored for API/OpenRouter backends (most API models don't support effort control). | `"medium"` |
| `context_weight` | Controls how much system context is injected into the prompt: `"light"`, `"standard"`, or `"full"`. See [Context weight](#context-weight) below. Default: `"full"`. | `"light"` |
| `force_command` | Enable `/<tier_name> <message>` to bypass routing and lock the session to this tier. The override persists until `/new` or session timeout. Works in Telegram and CC Chat. | `true` |
| `max_turns` | Maximum number of actions ALF can take in one response (reading files, running commands, etc.). Keeps things from running too long. 0 = no limit. | `10` |
| `max_iterations` | (Agent only) Max delegate→synthesize cycles. | `10` |
| `timeout_minutes` | (Agent only) Hard timeout in minutes. | `60` |
| `tools` | List of allowed tools for read-only tiers (e.g. `["Read"]`, `["Read", "WebSearch"]`). Write-capable tiers get all tools. | `["Read"]` |

## Example: simple two-tier setup

If you want to keep things minimal - one fast tier, one powerful tier:

```json
{
  "router_model": "haiku",
  "default_fallback": "base",
  "router_distinctions": "Use 'deep' for analysis, code, or complex tasks. Use 'base' for everything else.",
  "tiers": [
    {
      "name": "base",
      "model": "haiku",
      "priority": 1,
      "enabled": true,
      "routable": true,
      "router_label": "Quick responses, simple questions, casual chat",
      "effort": "low"
    },
    {
      "name": "deep",
      "model": "sonnet",
      "priority": 2,
      "enabled": true,
      "routable": true,
      "router_label": "Complex analysis, code generation, multi-step tasks",
      "write_capable": true,
      "effort": "medium",
      "max_turns": 10
    }
  ]
}
```

> This covers 90% of use cases. Start here and add tiers only when you notice the router making wrong choices.

## Example: adding a manual-only power tier

Want an Opus tier you can trigger manually, but the router never picks it automatically?

```json
{
  "name": "power",
  "model": "opus",
  "priority": 10,
  "enabled": true,
  "routable": false,
  "force_command": true,
  "router_label": "Deep expert analysis",
  "write_capable": true,
  "effort": "high",
  "max_turns": 20
}
```

Add this to your `tiers` array. Now type `/power analyze this system` in Telegram or CC Chat to use it. The router will never pick it on its own. Note: you must include a message after the command - `/power` alone shows a usage hint.

### Session locking

When you use a force command, the session **locks to that tier** for all subsequent messages. You can either include a message or just lock the tier for later:

```
You: /opus                                 → locks session to opus (no message sent)
You: analyze the auth module               → uses opus
You: what about the error handling?        → still uses opus
You: /new                                  → resets, back to normal routing
```

Or with a message in the same command:

```
You: /opus analyze the auth module         → locks session to opus + processes message
You: can you refactor the retry logic?     → still uses opus
```

ALF confirms the lock with a message: `⚡ Session locked to opus. Use /new to reset.`

The lock clears automatically when:
- You type `/new` (manual reset)
- The session times out (default: 30 minutes of inactivity)
- You use a different force command (switches to the new tier)

## Available tools

The `tools` field controls which tools are available to a tier. Behavior differs between CLI and API backends.

### CLI tiers (`backend: "cli"` or unset)

CLI tiers run Claude Code as a subprocess. Tools are managed by Claude Code itself - `toolbox.md` describes all available tools to the model. Write-capable tiers (`write_capable: true`) get all tools automatically via `--dangerously-skip-permissions`.

| Tool | What it does |
|------|-------------|
| `Read` | Read files - code, config, logs, images, PDFs |
| `Write` | Create or overwrite files |
| `Edit` | Modify existing files (text replacement) |
| `Bash` | Execute shell commands |
| `Glob` | Search files by pattern (e.g. `**/*.go`) |
| `Grep` | Search file contents with regex |
| `WebSearch` | Search the web for information |
| `WebFetch` | Fetch content from a URL |
| `NotebookEdit` | Edit Jupyter notebooks |
| `Agent` | Launch a sub-agent for complex tasks |

### API tiers (`backend: "openrouter"`, `"ollama"`, etc.)

API tiers work differently from CLI tiers. ALF gives the model a list of available tools, and the model can call them one at a time until it has enough information to respond (or reaches the `max_turns` limit).

Available tools for API tiers:

| Type | Examples | How they work |
|------|----------|---------------|
| **Native tools** | `bash`, `read_file`, `grep`, `glob`, `write_file` | Built-in Go implementations. Always have proper schemas with full descriptions. |
| **User tools with schema** | Any script in `tools/` that has a matching `.json` manifest | The JSON manifest provides the tool name, description, and parameter schema. |
| **User tools without schema** | Scripts in `tools/` or `tools.d/` without a `.json` file | **Not available to API tiers.** These tools lack descriptions, so API models can't reason about when to use them. They remain available to CLI tiers via `toolbox.md`. |

### Tool wildcards

| Value | Resolves to |
|-------|-------------|
| `["*"]` | All native tools + user tools **that have a `.json` schema**. Best for capable models. |
| `["*native"]` | Only native tools (`bash`, `read_file`, `grep`, `glob`, `write_file`). Best for weaker/free models that struggle with tool selection. |
| `["bash", "grep"]` | Only the listed tools, if they have a schema. |

> **Tip:** Free or lightweight models (e.g. `step-3.5-flash:free`) work best with `["*native"]`. Giving them too many tools causes confusion - they pick random tools instead of the right one.

### Making a user tool available to API tiers

Create a JSON manifest next to your script. For example, if you have `tools/my-tool`, create `tools/my-tool.json`:

```json
{
  "name": "my_tool",
  "description": "Clear description of what this tool does and when to use it",
  "parameters": {
    "type": "object",
    "properties": {
      "args": {
        "type": "string",
        "description": "Command-line arguments"
      }
    },
    "required": ["args"],
    "additionalProperties": false
  }
}
```

The description is critical - it's the only context the API model has to decide whether to call your tool.

### Typical combinations

- **Weak model, basic tasks:** `["*native"]`
- **Capable model, full access:** `["*"]`
- **Read-only analysis (CLI):** `["Read", "Glob", "Grep"]`
- **Specific tools only:** `["bash", "read_file", "remember"]`

## Context weight

The `context_weight` field controls how much system prompt content is injected for a tier. Lighter models (Haiku, GPT-4o-mini, small Grok variants) perform better with less noise in the context — they focus on the conversation instead of drowning in instructions.

| Weight | What's injected | What's skipped | Best for |
|--------|----------------|----------------|----------|
| `"light"` | Identity, filesystem basics, secrets rule, soul.md, mood.md, index.md | Toolbox, skill catalog, storage/lookup protocols, memory tools, tool reminder, reaction format, tool_use/result in conversation history | Fast/cheap models handling simple tasks |
| `"standard"` | Everything except `@weight full` tagged sections | Only sections explicitly tagged for full weight | Mid-range models |
| `"full"` (default) | Everything | Nothing | Capable models (Sonnet, Opus, GPT-4) |

**How it affects the router:** The router sees which tiers are light and labels them as "light model — simple tasks only" in its classification prompt. It also has a programmatic guardrail: if a message shows complexity markers (question words like "why"/"how", length > 150 chars, multiple questions), it automatically upgrades from a light tier to the next one.

**Example - Haiku as a light tier:**
```json
{
  "name": "haiku",
  "model": "haiku",
  "context_weight": "light",
  "effort": "low"
}
```

> **Tip:** Set `context_weight` to `"light"` on any tier using a small/cheap model. The reduction from ~4500 to ~2000 system tokens noticeably improves coherence on the last 5-10 messages.

---

## Common questions

**How do I know which tier handled my message?**
In the CC Chat tab, each reply shows the tier name and model in the metadata below the message. In Telegram, ALF shows a status message like "Processing with sonnet".

**ALF keeps using Sonnet for simple questions. How do I fix it?**
Make your `router_label` for Haiku tiers more descriptive. Be specific:
- Bad: `"Simple tasks"`
- Good: `"Casual conversation, greetings, yes/no questions, translations, jokes, weather"`

Also check `router_distinctions` - add a rule like "Default to haiku for anything conversational."

**What's the difference between `enabled: false` and `routable: false`?**
- `enabled: false` - tier is completely off. Nobody can use it.
- `routable: false` - router ignores it, but you can still force it with a command.

**Should I use `max_turns`?**
Yes, always on write-capable tiers. Without it, a write tier could loop endlessly trying to complete a task. Start with 5-10 for most tiers, 20 for complex ones.

**What does `effort` actually do?**
It controls how much "thinking" the model does before answering. `low` = quick gut reaction. `medium` = balanced. `high` = deep reasoning. Higher effort means slower (and slightly more expensive) responses.

> **Note:** `effort` only works with **CLI tiers** (Claude's `--effort` flag). For API/OpenRouter backends, this field is ignored - most API models (Grok, DeepSeek, Gemini, etc.) don't support effort control. To differentiate API tiers, use different models or adjust `max_turns` and `router_label` instead.

## Media routing

When you send a photo, document, or video:

- **With a caption** - the router classifies the caption to pick the right tier, then verifies the tier can view images (has Read tool or is write-capable). If not, it upgrades to the cheapest image-capable tier.
- **Without a caption** - goes directly to the cheapest non-instant tier with image support.

This means sending a screenshot with "fix this bug" routes to `sonnet`, not `haiku`.

## Using multiple tier configurations

You can maintain several tier files and switch between them without editing `tiers.json`.

Set `tiers_file` in `config.json` (via **Settings → Configuration**):

```json
{
  "tiers_file": "tiers-minimal.json"
}
```

Relative paths are resolved inside `config.d/`. Absolute paths are used as-is.
When you save the change, ALF reloads immediately from the new file - no restart needed.
The file watcher also tracks the new path, so future edits to it hot-reload as expected.

To go back to the default, set `"tiers_file": "tiers.json"`.

See [Configuration Reference](docs:config) for all `config.json` fields.

## Tips for good routing

1. **Write detailed `router_label` values.** This is the #1 thing the router looks at. Be specific.
2. **Keep `router_distinctions` clear.** Write it like you're explaining to a new employee.
3. **Test after changes.** Send a mix of simple and complex messages. Check which tier picks up each one.
4. **Start simple.** Two tiers work great. Only add more when you see a real need.
5. **Use `max_turns` on all write tiers.** Safety first.

## What's next?

- [Getting Started](docs:getting-started) - ALF setup and overview
- [Agent Teams](docs:agent-teams) - coordinate multiple agents for complex tasks
- [Creating Skills](docs:creating-skills) - teach ALF new abilities with auto-injection
