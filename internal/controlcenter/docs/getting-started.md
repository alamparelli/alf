---
category: Basics
tags: quickstart, tiers, commands, control center, workspace, teach
order: 1
---

# Getting Started

Welcome to ALF's documentation. If you're reading this, you're already set up and logged in — so let's skip the installation and get straight to what you can do.

## Talking to ALF

Send a message on Telegram or use the Chat tab here. ALF reads your message, picks the right model, and replies.

That's the basic loop. But there's a lot more going on under the hood.

## How ALF picks the right model

ALF uses a system called **tiers**. Each tier is a different configuration — a Claude model with specific capabilities.

When you send a message, a fast **router** classifies it and picks a tier:

| You send... | ALF picks... | Why |
|-------------|-------------|-----|
| "Hey!" | instant (Haiku) | Simple greeting, instant reply |
| "What's the weather?" | haiku | Quick question, read-only |
| "Review this code" | sonnet | Needs analysis |
| "Refactor the auth system" | opus | Complex, needs file access |
| "Research X and write a report" | agent | Multi-step, needs agent coordination |

When you send a photo with a caption, the router classifies based on the caption text and ensures the chosen tier can view images. Photos without a caption go to the cheapest image-capable tier.

### Overriding the router

Tiers with `force_command: true` can be called directly. Type `/<tier_name> <message>`:

```
/sonnet fix the bug in router.go
/opus explain the architecture of this project
```

This bypasses the router entirely — useful when you know you need a specific model.

> See [Setting Up Tiers](docs:tier-setup) to customize which models ALF uses and when.

## The Control Center

You're looking at it. Here's what each section does:

| Tab | What it does |
|-----|-------------|
| **Home** | Status, configuration, Workspace Explorer, Teach |
| **Chat** | Browser-based chat (same as Telegram, different interface) |
| **Tasks** | Monitor agent tasks — running, completed, failed |
| **Apps** | Self-contained apps ALF generates (appears when apps exist) |
| **Terminal** | Shell session inside the Docker container ([details](docs:terminal)) |
| **Logs** | Daemon logs and event viewer |
| **Docs** | You are here |

## The Workspace Explorer

On the Home tab, scroll down to **Workspace**. This is a file browser for ALF's data:

| Folder | What's inside | Example |
|--------|-------------|---------|
| `config.d/` | Tiers, config, agent teams | `tiers.json`, `agents/starter.json` |
| `context/` | Files added to every conversation | `index.md`, `project-notes.md` |
| `skills/` | Custom skills ALF can use | `x-manager/SKILL.md` |
| `tools/` | Custom scripts and executables | `disk-check` |
| `apps/` | Self-contained apps | `trend-radar/index.html` |
| `logs/` | Daemon logs and event history | `daemon.log`, `events/` |

You can create, edit, and delete files directly from here. Changes are picked up automatically.

## Teaching ALF

Want ALF to remember something? Use the **Teach** feature on the Home tab.

1. Pick a destination: **Memory** (ALF remembers it) or **Context file** (injected into every conversation)
2. Choose how to process it: extract key facts, preferences, decisions, or store as-is
3. Paste your content (meeting notes, docs, anything)
4. Click **Import**

Example: paste meeting notes, pick "Extract key facts", and ALF pulls out action items and stores them in memory.

## Scheduling tasks

ALF can run tasks automatically on a schedule. Ask ALF to create one for you, or use the `schedule` tool directly.

### Ask ALF to schedule something

Just tell ALF what you want in natural language:

```
Schedule a daily morning briefing at 9 AM that summarizes my priorities
```

```
Every Monday at 8 AM, check for new GitHub issues and send me a summary
```

ALF will use the `schedule create` tool with the right parameters.

### Schedule tool reference

Two types of scheduled jobs:

**LLM jobs** — ALF thinks and responds using a prompt:
```bash
schedule create --name "morning brief" --schedule "0 0 9 * * 1-5" \
  --tier sonnet --prompt "Summarize today's priorities" --output telegram
```

**Direct jobs** — run a bash command, no LLM involved:
```bash
schedule create --name "disk check" --schedule "0 0 */6 * * *" \
  --command "df -h" --output telegram
```

**Agent jobs** — coordinate multiple agents for complex tasks:
```bash
schedule create --name "weekly report" --schedule "0 0 9 * * 1" \
  --tier agent --prompt "Analyze this week's commits and write a report" \
  --output telegram
```

### Schedule options

| Option | Required | What it does |
|--------|----------|-------------|
| `--name` | Yes | Job name (for identification) |
| `--schedule` | Yes | Cron expression or one-shot datetime |
| `--tier` | For LLM | Which tier to use (`haiku`, `sonnet`, `agent`, etc.) |
| `--prompt` | For LLM | What to ask the model |
| `--command` | For direct | Bash command to execute |
| `--output` | No | Where to send results: `telegram`, `file`, `both`, `silent` (default: `telegram`) |
| `--skills` | No | Comma-separated skill names to inject |

### Schedule expressions

```
Seconds Minutes Hours DayOfMonth Month DayOfWeek
```

| Expression | Meaning |
|-----------|---------|
| `0 0 9 * * 1-5` | Every weekday at 9:00 AM |
| `0 30 8 * * 1` | Every Monday at 8:30 AM |
| `0 0 */6 * * *` | Every 6 hours |
| `0 */30 * * * *` | Every 30 minutes |
| `2026-03-15T14:00:00Z` | One-shot at a specific time (RFC3339) |

### Managing scheduled jobs

```bash
schedule list                           # List all jobs
schedule list --user                    # List user-created jobs only
schedule update <id> --enabled false    # Disable a job
schedule update <id> --schedule "..."   # Change the schedule
schedule delete <id>                    # Remove a job
```

## Useful commands

These work in both Telegram and CC Chat:

| Command | What it does |
|---------|-------------|
| `/start` | Run the welcome onboarding again |
| `/new` | Clear the conversation and start fresh |
| `/login` | Get a new magic link for the Control Center |
| `/<tier_name>` | Force a specific tier (e.g. `/opus fix this bug`) |

## Something not working?

| Problem | What to try |
|---------|------------|
| ALF doesn't reply on Telegram | Check Logs tab for errors. Ask your admin to run `alf status`. |
| ALF picks the wrong tier | Edit `tiers.json` in Workspace. Make sure `router_label` descriptions are clear. |
| ALF is slow | You might be hitting a powerful tier for simple messages. Check tier setup. |
| Scheduled job didn't run | Check Logs tab. Verify the cron expression. Use `schedule list` to check next run time. |

## Customizing the deployment

ALF's `docker-compose.yml` is auto-generated and regenerated on upgrades. **Do not edit it directly** — your changes will be overwritten.

For custom configuration (extra volumes, networks, labels, environment variables), create a `docker-compose.override.yml` in the same directory. Docker Compose automatically merges both files:

```yaml
# docker-compose.override.yml
services:
  alf:
    environment:
      - MY_CUSTOM_VAR=value
    volumes:
      - /my/data:/mnt/data
```

To regenerate the base file manually (e.g. after adding a secret): `alf compose`

## What's next?

- [Setting Up Tiers](docs:tier-setup) — customize which models ALF uses and when
- [Managing Conversations](docs:sessions) — sessions, `/new`, and context management
- [Creating Skills](docs:creating-skills) — teach ALF new abilities with auto-injection
- [Agent Teams](docs:agent-teams) — coordinate multiple agents for complex tasks
- [Building Tools & Extensions](docs:container-packages) — install packages, create tools, build apps
