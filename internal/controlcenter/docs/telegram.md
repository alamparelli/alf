---
category: Configuration
tags: telegram, bot, mobile, notifications
order: 7
---

# Telegram

Connect Telegram to chat with ALF from your phone and receive notifications when scheduled jobs complete, tasks finish, or the system needs attention.

## Why connect Telegram?

- **Mobile access** -- send prompts and get responses from anywhere.
- **Notifications** -- schedule outputs, task completions, and system alerts are pushed to your chat.
- **Always on** -- Telegram works even when the Control Center browser tab is closed.

## Setup

### 1. Create a bot

1. Open Telegram and search for [@BotFather](https://t.me/BotFather).
2. Send `/newbot` and follow the prompts to pick a name and username.
3. BotFather replies with a **bot token** (looks like `123456789:ABCdef...`). Copy it.

### 2. Get your chat ID

You need your personal numeric chat ID so ALF knows where to send messages.

The easiest method: message [@userinfobot](https://t.me/userinfobot) on Telegram. It replies with your chat ID.

### 3. Configure in ALF

1. Open the **Settings** tab in the Control Center sidebar.
2. Scroll to **Telegram Integration**. If not yet configured, the form is already expanded.
3. Paste your **Bot Token** and **Chat ID**.
4. Click **Save & Verify**. ALF validates the token against the Telegram API. If valid, you see a success message with your bot's username.
5. **Restart ALF** for the connection to activate. Use the Restart button in the System section above.

> Already configured? Click **Edit** on the Telegram card to update credentials, or **Disconnect** to remove them entirely.

## Testing the connection

After restarting, send any message to your bot in Telegram. ALF should reply. If it does not respond:

- Double-check the bot token and chat ID in Settings.
- Make sure you started a conversation with the bot (send `/start` first).
- Check **Logs** for Telegram-related errors.

## What works in Telegram vs Control Center

Telegram operates in **plain text mode**. Messages you send and receive are unformatted -- no markdown rendering, no code highlighting, no interactive elements.

For tasks that produce structured output (tables, code blocks, long reports), the Control Center gives a better reading experience. Use Telegram for quick commands, short answers, and on-the-go access.

## Notifications

ALF pushes several types of notifications to Telegram automatically:

| Notification | When |
|-------------|------|
| **Schedule output** | A scheduled job completes with output set to `chat` or `both` |
| **Turn limit reached** | A scheduled job runs out of LLM turns |
| **Update available** | A newer ALF version is detected (if enabled in Settings) |
| **Daily digest** | Summary of all scheduled job executions from the past 24 hours |

## Quiet hours

You can configure quiet hours to suppress Telegram notifications during specific times. This is set in `config.json` via the Control Center config editor.

See [Configuration](config.md) for details on the quiet hours fields.

## Disconnecting

1. Go to **Settings** > **Telegram Integration**.
2. Click **Edit**, then **Disconnect**.
3. Confirm the prompt. ALF switches to Control Center-only mode after the next restart.

The bot token and chat ID are removed from the encrypted vault. You can reconnect at any time by repeating the setup steps.

## Common questions

**Can I use a group chat instead of a private chat?**
ALF is designed for a single private chat. Group chats are not supported.

**Is the bot token stored securely?**
Yes. The token and chat ID are stored in ALF's encrypted vault, not in plain-text config files.

**Do I need to restart after every change?**
Yes. Telegram credentials are loaded at daemon startup. Any change requires a restart to take effect.

## What's next

- [Schedules](schedules.md) -- set up automated jobs that send results to Telegram
- [Configuration](config.md) -- quiet hours and other notification settings
