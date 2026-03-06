---
category: Setup
tags: install, init, quickstart, telegram, control center
order: 1
---

# Getting Started

Set up your personal AI assistant in under 5 minutes.

## What is ALF?

ALF is your personal AI assistant. It runs on your own server and talks to you through Telegram. Think of it like having a smart helper that you fully control — your data stays yours.

Here's what makes it special:

- **Talks to you on Telegram** — just send a message, like texting a friend
- **Picks the right brain for the job** — simple questions get fast answers, complex ones get deeper thinking
- **Learns about you over time** — remembers your preferences and context
- **Has a web dashboard** — manage everything from your browser

## Quick start

### Step 1: Install

Download the `alf` command-line tool:

```bash
go install github.com/alamparelli/alf/cmd/alf@latest
```

### Step 2: Set up

Run the setup wizard. It asks you a few questions:

```bash
alf init
```

You'll need:

| What | Where to get it |
|------|-----------------|
| **Telegram Bot Token** | Open Telegram, search for [@BotFather](https://t.me/BotFather), send `/newbot` |
| **Your Chat ID** | Open Telegram, search for [@userinfobot](https://t.me/userinfobot), send any message |

> The wizard also asks for a **data directory** (where ALF stores its files, default: `~/.alf`) and a **port** for the web dashboard (default: `8080`).

### Step 3: Start

```bash
alf start
```

That's it! ALF pulls its Docker image, starts up, and sends you a welcome message on Telegram.

### Step 4: Say hello

Open Telegram and send any message to your bot. You should get a reply within seconds.

## How does ALF decide which model to use?

ALF uses a system called **tiers**. Each tier is a different configuration — a combination of a Claude model, speed, and capabilities.

Here's an analogy: imagine you have three assistants.

- **The quick one** (Haiku) — great for "yes/no", casual chat, simple questions. Fast and cheap.
- **The smart one** (Sonnet) — handles code review, analysis, writing. A good balance.
- **The expert** (Opus) — for complex architecture, deep research, big refactoring. Powerful but slower.

When you send a message, a fast **router** reads it and picks the best tier. For example:

| You send... | ALF picks... | Why |
|-------------|-------------|-----|
| "Hey!" | instant (Haiku) | Simple greeting, fast reply |
| "What's the weather like?" | haiku_r | Quick factual question |
| "Review this code for bugs" | sonnet_r | Needs analysis and reasoning |
| "Refactor the auth system" | opus_rw | Complex, multi-file changes |

> You can override the router. Tiers with `force_command: true` can be invoked directly — type `/<tier_name> <message>` (e.g. `/sonnet_rw fix this bug`) and ALF uses that tier for your message.

Want to customize tiers? See [Setting Up Tiers](docs:tier-setup).

## The Control Center

The Control Center (CC) is your web dashboard. Open it at:

```
http://your-server:<port>
```

The port is whatever you chose during `alf init` (default: `8080`).

### How to log in

1. Send `/login` to ALF on Telegram
2. You'll get a magic link — click it
3. You're in!

### What you'll find

| Tab | What it does |
|-----|-------------|
| **Home** | See ALF's status, uptime, and message count. Edit configuration. Browse and edit files. Import knowledge. |
| **Chat** | Chat with ALF from your browser (same as Telegram, different interface) |
| **Pages** | Dynamic dashboards that ALF generates for you (appear in the sidebar when pages exist) |
| **Docs** | You are here! |

### The Workspace Explorer

On the Home tab, scroll down to **Workspace**. This is a file browser for everything ALF stores:

| Folder | What's inside | Example |
|--------|-------------|---------|
| `config.d/` | Settings and tier configuration | `tiers.json`, `config.json` |
| `context.d/` | Files added to every conversation | `project-notes.md` |
| `skills.d/` | Custom skills ALF can use | `security-audit/SKILL.md` |
| `tools/` | Tool definitions | `web-search.json` |
| `memory.d/` | Facts ALF has learned about you | Auto-generated |
| `pages/` | HTML dashboards | `status-board.html` |
| `logs/` | Conversation history | Auto-generated |

### Teaching ALF

Want ALF to remember something? Use the **Teach** feature on the Home tab.

1. Pick a destination: **Memory** (ALF remembers it) or **Context file** (added to every conversation)
2. Choose how to process it: extract key facts, preferences, decisions, or store as-is
3. Paste your content (meeting notes, docs, anything)
4. Click **Import**

Example: paste your meeting notes, pick "Extract key facts", and ALF will pull out the important points and remember them.

## Useful commands

These work in both Telegram and the CC Chat tab:

| Command | What it does |
|---------|-------------|
| `/start` | Run the welcome onboarding again |
| `/new` | Clear the conversation and start fresh |
| `/login` | Get a link to open the Control Center |

## Keeping ALF up to date

```bash
alf upgrade
```

This downloads the latest version and restarts ALF. Your data and settings are preserved.

## Something not working?

| Problem | What to try |
|---------|------------|
| ALF doesn't reply on Telegram | Run `alf status` to check if it's running. Look at logs in the Workspace Explorer. |
| Can't open the Control Center | Make sure the port is right and not blocked. Check Docker with `docker logs alf`. |
| ALF picks the wrong tier | Edit `tiers.json` in Workspace. Check that `router_label` descriptions are clear. See [Setting Up Tiers](docs:tier-setup). |
| ALF is slow to respond | You might be hitting a powerful tier for simple messages. Check your tier setup — make sure Haiku handles casual chat. |

## What's next?

- [Setting Up Tiers](docs:tier-setup) — customize which models ALF uses and when
