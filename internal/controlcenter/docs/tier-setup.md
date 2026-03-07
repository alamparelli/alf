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

**Option 1 — Tiers tab (recommended).** Open the Control Center sidebar and click **Tiers**. You'll see all tiers in a visual list. Click **Add Tier** or **Edit** to use the form with dropdowns, checkboxes, and tool selection.

**Option 2 — Workspace editor.** Go to **Home > Workspace**, navigate to `config.d/tiers.json`, and edit the JSON directly.

Changes take effect immediately — no restart needed.

## The default setup

ALF comes with 7 tiers out of the box. Here's what each one does:

| Tier | Model | What it handles | Can edit files? |
|------|-------|----------------|:-:|
| `instant` | Haiku | "Hi", "thanks", "ok", yes/no | No |
| `haiku_r` | Haiku | Chat, Q&A, summaries, translations | No |
| `haiku_rw` | Haiku | Run tools, schedules, quick edits | Yes (max 5 steps) |
| `sonnet_r` | Sonnet | Code review, analysis, research | No |
| `sonnet_rw` | Sonnet | Write code, fix bugs, create scripts | Yes (max 10 steps) |
| `opus_r` | Opus | Architecture, deep analysis, strategy | No |
| `opus_rw` | Opus | Large refactoring, complex features | Yes (max 20 steps) |
| `agent` | Opus | Multi-agent coordination for complex tasks | Via agents |

The agent tier is **disabled by default**. Enable it in `tiers.json` to let the router automatically delegate complex tasks to agent teams. See [Agent Teams](docs:agent-teams) for setup.

> Most of your messages will be handled by Haiku — it's fast and cheap. Sonnet and Opus only kick in when needed.

## How the router decides

The router follows these rules:

1. **Simple question?** Use Haiku. Chat, facts, jokes, translations — all Haiku.
2. **Needs thinking?** Use Sonnet. Code review, analysis, writing structured content.
3. **Really complex?** Use Opus. Only for system-wide architecture or holding many constraints at once.
4. **Need to change files?** Use a `_rw` tier. Only when you explicitly ask to create, edit, or delete something.

> If the router can't decide, it falls back to `haiku_r`. Fast and safe.

## Understanding the config file

Here's what a `tiers.json` file looks like:

```json
{
  "router_model": "haiku",
  "default_fallback": "haiku_r",
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
| `default_fallback` | Which tier to use when the router can't decide. | `"haiku_r"` |
| `router_distinctions` | Plain English rules for the router. This is your main control lever. | `"Use haiku for simple tasks..."` |

### Per-tier settings

| Setting | What it does | Example |
|---------|-------------|---------|
| `name` | Unique name. Shows up in logs and status messages. | `"sonnet_rw"` |
| `model` | Which Claude model to use: `haiku`, `sonnet`, or `opus`. | `"sonnet"` |
| `priority` | Lower number = higher priority. Used for fallback selection and media routing. | `3` |
| `enabled` | Set to `false` to turn off a tier completely. | `true` |
| `routable` | Set to `false` to hide from the router (manual-only). | `true` |
| `instant` | Router answers directly without spawning a Claude session. Used for greetings and one-liners. | `false` |
| `router_label` | Describes what this tier is good at. The router reads this to decide. **This is the most important field.** | `"Code review, debugging"` |
| `description` | Optional longer description. Falls back to `router_label` if not set. Used in router prompt. | `"Deep code analysis"` |
| `write_capable` | Can this tier create, edit, or delete files? | `false` |
| `effort` | How hard the model thinks: `low`, `medium`, or `high`. | `"medium"` |
| `force_command` | Enable `/<tier_name> <message>` to bypass routing and force this tier. Works in Telegram and CC Chat. | `true` |
| `max_turns` | Max steps for tool use. Prevents runaway loops. 0 = unlimited. | `10` |
| `max_iterations` | (Agent only) Max delegate→synthesize cycles. | `10` |
| `timeout_minutes` | (Agent only) Hard timeout in minutes. | `60` |
| `tools` | List of allowed tools for read-only tiers (e.g. `["Read"]`, `["Read", "WebSearch"]`). Write-capable tiers get all tools. | `["Read"]` |

## Example: simple two-tier setup

If you want to keep things minimal — one fast tier, one powerful tier:

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

Add this to your `tiers` array. Now type `/power analyze this system` in Telegram or CC Chat to use it. The router will never pick it on its own. Note: you must include a message after the command — `/power` alone shows a usage hint.

## Available tools

The `tools` field controls which Claude Code tools a read-only tier can use. Write-capable tiers (`write_capable: true`) get all tools automatically.

| Tool | What it does |
|------|-------------|
| `Read` | Read files — code, config, logs, images, PDFs |
| `Write` | Create or overwrite files |
| `Edit` | Modify existing files (text replacement) |
| `Bash` | Execute shell commands |
| `Glob` | Search files by pattern (e.g. `**/*.go`) |
| `Grep` | Search file contents with regex |
| `WebSearch` | Search the web for information |
| `WebFetch` | Fetch content from a URL |
| `NotebookEdit` | Edit Jupyter notebooks |
| `Agent` | Launch a sub-agent for complex tasks |

**Typical combinations:**
- Read-only analysis: `["Read"]`
- Read + web research: `["Read", "WebSearch"]`
- Full read access: `["Read", "Glob", "Grep", "WebSearch", "WebFetch"]`

> Write-capable tiers don't need a `tools` list — they get everything via `--dangerously-skip-permissions`.

## Common questions

**How do I know which tier handled my message?**
In the CC Chat tab, each reply shows the tier name and model in the metadata below the message. In Telegram, ALF shows a status message like "Processing with sonnet_r".

**ALF keeps using Sonnet for simple questions. How do I fix it?**
Make your `router_label` for Haiku tiers more descriptive. Be specific:
- Bad: `"Simple tasks"`
- Good: `"Casual conversation, greetings, yes/no questions, translations, jokes, weather"`

Also check `router_distinctions` — add a rule like "Default to haiku for anything conversational."

**What's the difference between `enabled: false` and `routable: false`?**
- `enabled: false` — tier is completely off. Nobody can use it.
- `routable: false` — router ignores it, but you can still force it with a command.

**Should I use `max_turns`?**
Yes, always on write-capable tiers. Without it, a write tier could loop endlessly trying to complete a task. Start with 5-10 for most tiers, 20 for complex ones.

**What does `effort` actually do?**
It controls how much "thinking" the model does before answering. `low` = quick gut reaction. `medium` = balanced. `high` = deep reasoning. Higher effort means slower (and slightly more expensive) responses.

## Media routing

When you send a photo, document, or video:

- **With a caption** — the router classifies the caption to pick the right tier, then verifies the tier can view images (has Read tool or is write-capable). If not, it upgrades to the cheapest image-capable tier.
- **Without a caption** — goes directly to the cheapest non-instant tier with image support.

This means sending a screenshot with "fix this bug" routes to `sonnet_rw`, not `haiku_r`.

## Tips for good routing

1. **Write detailed `router_label` values.** This is the #1 thing the router looks at. Be specific.
2. **Keep `router_distinctions` clear.** Write it like you're explaining to a new employee.
3. **Test after changes.** Send a mix of simple and complex messages. Check which tier picks up each one.
4. **Start simple.** Two tiers work great. Only add more when you see a real need.
5. **Use `max_turns` on all write tiers.** Safety first.

## What's next?

- [Getting Started](docs:getting-started) — ALF setup and overview
- [Agent Teams](docs:agent-teams) — coordinate multiple agents for complex tasks
- [Creating Skills](docs:creating-skills) — teach ALF new abilities with auto-injection
