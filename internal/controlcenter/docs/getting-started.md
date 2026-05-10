---
category: Basics
tags: quickstart, tiers, commands, control center, workspace, teach
order: 1
---

# Getting Started

Welcome to ALF. This guide walks you through everything you need to know to get the most out of your setup.

## First things first

If you haven't already, talk to ALF - on Telegram or in the **Chat** tab here. The first conversation is a quick onboarding where ALF learns who you are, what you need help with, and how you want it to communicate. This shapes its personality and behavior going forward.

After that, just send messages. ALF reads what you write, picks the right model, and replies.

That's the basic loop. But there's a lot more going on under the hood.

## First steps checklist

Here's what to do after your initial setup:

1. **Complete the onboarding** - send a message to ALF (Telegram or Chat tab). It will ask a few questions to personalize itself.
2. **Set up the Vault** - go to the **Vault** tab and create a master password. The vault stores API keys, credentials, and secrets encrypted at rest. ALF uses it to securely manage service integrations.
3. **Review your tiers** - open the **Tiers** tab to see which models ALF uses. The defaults work well, but you can tune them.
4. **Explore the workspace** - the **Home** tab has a file browser where you can see and edit ALF's configuration, context files, and skills.

Everything else - scheduling, skills, agent teams - you can explore as you need it.

## How ALF picks the right model

ALF uses a system called **tiers**. Each tier is a different configuration - a Claude model with specific capabilities.

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

This bypasses the router entirely - useful when you know you need a specific model.

> See [Setting Up Tiers](docs:tier-setup) to customize which models ALF uses and when.

## The Control Center

You're looking at it. Here's what each section does:

| Tab | What it does |
|-----|-------------|
| **Home** | Admin actions, Workspace Explorer, Teach |
| **Chat** | Browser-based chat with multiple conversation tabs. Each tab is a separate thread with its own history. |
| **Terminal** | Shell session inside the Docker container ([details](docs:terminal)) |
| **Tasks** | Monitor agent tasks - running, completed, failed. Launch new tasks. |
| **Schedules** | Create, edit, and monitor scheduled jobs ([details](docs:schedules)) |
| **Logs** | Daemon logs with search and session filtering ([details](docs:logs)) |
| **Tiers** | Configure response tiers in real-time ([details](docs:tier-setup)) |
| **Firewall** | Network firewall rules ([details](docs:firewall)) |
| **Vault** | Secrets vault - store credentials, OAuth2 tokens, files ([details](docs:vault)) |
| **Settings** | Configuration, Telegram setup, admin actions ([details](docs:config)) |
| **Docs** | You are here |
| **Apps** | Self-contained apps ALF generates (appears when apps exist) |

## Chat conversations

The **Chat** tab supports multiple conversations at once, like browser tabs.

| Action | How |
|--------|-----|
| **Start a new conversation** | Click the **+** button next to the tabs |
| **Switch between conversations** | Click a tab |
| **Rename a conversation** | Double-click the tab name and type a new one |
| **Close a conversation** | Click the **x** on the tab |

Each conversation has its own message history and runs independently. Your tabs are saved automatically - they'll still be there when you come back.

> **Tip:** Use `/new` inside a tab to clear it and start fresh without creating a new tab. The tab stays, but the conversation resets.

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

**LLM jobs** - ALF thinks and responds using a prompt:
```bash
schedule create --name "morning brief" --schedule "0 0 9 * * 1-5" \
  --tier sonnet --prompt "Summarize today's priorities" --output chat
```

**Direct jobs** - run a bash command, no LLM involved:
```bash
schedule create --name "disk check" --schedule "0 0 */6 * * *" \
  --command "df -h" --output chat
```

**Agent jobs** - coordinate multiple agents for complex tasks:
```bash
schedule create --name "weekly report" --schedule "0 0 9 * * 1" \
  --tier agent --prompt "Analyze this week's commits and write a report" \
  --output chat
```

### Schedule options

| Option | Required | What it does |
|--------|----------|-------------|
| `--name` | Yes | Job name (for identification) |
| `--schedule` | Yes | Cron expression or one-shot datetime |
| `--tier` | For LLM | Which tier to use (`haiku`, `sonnet`, `agent`, etc.) |
| `--prompt` | For LLM | What to ask the model |
| `--command` | For direct | Bash command to execute |
| `--message` | For reminder | Text to send directly (no LLM, no bash) |
| `--timeout` | No | Max execution time (e.g. `5m`, `30s`). Default varies by tier. |
| `--output` | No | Where to send results: `chat` (TG+CC), `tg`, `cc`, `file`, `both` (chat+file), `silent` (default: `chat`) |
| `--skills` | No | Comma-separated skill names to inject |

### Schedule expressions

Schedules use a 6-part format: `seconds minutes hours day month weekday`

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
| `/new` or `/clear` | Clear the conversation and start fresh |
| `/login` | Get a new magic link for the Control Center |
| `/<tier_name>` | Force a specific tier (e.g. `/opus fix this bug`) - session locks to that tier |
| `/<tier_name>` (no message) | Lock the session to a tier without sending a message |

**Telegram-only commands:**

| Command | What it does |
|---------|-------------|
| `/help` | Show all available commands |
| `/jobs` | List running agent jobs |
| `/cancel` | Cancel all running agent jobs |
| `/restart` | Restart the ALF daemon |

**Desktop notifications** - ALF sends a browser notification when a response arrives while the Control Center is in the background. Allow notifications when your browser asks.

## Something not working?

| Problem | What to try |
|---------|------------|
| ALF doesn't reply on Telegram | Check Logs tab for errors. Ask your admin to run `alf status`. |
| ALF picks the wrong tier | Edit `tiers.json` in Workspace. Make sure `router_label` descriptions are clear. |
| ALF is slow | You might be hitting a powerful tier for simple messages. Check tier setup. |
| Scheduled job didn't run | Check Logs tab. Verify the cron expression. Use `schedule list` to check next run time. |

## How tools and apps stay safe

Anything ALF runs for you — tools, apps, skills — passes through 3 layers:

1. **Walls** — code runs inside ALF's sandbox; WASM bundles get an extra wall around their own module.
2. **Identity** — every loadable bundle is signed. ALF refuses to load anything unsigned. There is no dev-mode bypass.
3. **Authority** — a bundle only gets the permissions its manifest declares. Everything else is unreachable.

The doctrine (`ARCHITECTURE-SECURITY.md §4.1`): **anything loaded from disk at runtime is WASM-kind.** Bash, Python, and Go tools/apps only exist for code that ships inside the daemon binary itself (the maintainer-built path). When you ask ALF to "create a tool", it produces a WASM bundle in `~/data/tools/<id>/` (or `~/data/apps/<slug>/` for long-running apps), the daemon auto-signs at boot, and the bundle runs isolated.

See [Isolation Model](docs:isolation-model) for the full mental model.

## Customizing the deployment

ALF's `docker-compose.yml` is auto-generated and regenerated on upgrades. **Do not edit it directly** - your changes will be overwritten.

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

## Learn more

- **[Chat](docs:chat)** — conversations, media upload, reactions, internal links
- **[Workspace](docs:workspace)** — file browser, JSON viewer, editor
- **[Tasks](docs:tasks)** — background agent work, approval flow, team delegation
- **[Spotlight Search](docs:spotlight-search)** — quick search across apps, files, and docs
- **[Schedules](docs:schedules)** — automate prompts and commands on a cron schedule
- **[Marketplace](docs:marketplace-guide)** — browse and install apps
- **[Telegram](docs:telegram)** — connect your phone for mobile chat and notifications

## What's next?

- [Setting Up Tiers](docs:tier-setup) - customize which models ALF uses and when
- [Configuration Reference](docs:config) - all `config.json` fields explained
- [Managing Conversations](docs:sessions) - sessions, `/new`, and context management
- [Creating Skills](docs:creating-skills) - teach ALF new abilities with auto-injection
- [Agent Teams](docs:agent-teams) - coordinate multiple agents for complex tasks
- [Building Tools & Extensions](docs:container-packages) - install packages, create tools, build apps
- [Isolation Model](docs:isolation-model) - the 3 layers, the kind decision tree, and the trust model
- [Creating WASM Tools](docs:wasm-tools) - isolated tools and apps for third-party and LLM-authored code
