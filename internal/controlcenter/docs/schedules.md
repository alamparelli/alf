---
category: Features
tags: schedules, cron, jobs, automation, recurring, commands
order: 4
---

# Schedules

Run prompts or bash commands automatically on a schedule.

## Quick start

1. Open the **Schedules** tab in the sidebar.
2. Click **Add Job**.
3. Give it a name, a cron schedule, and a prompt.
4. Click **Add**. The job runs automatically at the scheduled time.

## What can you schedule?

Two types of jobs:

| Type | Tier | What it does |
|------|------|-------------|
| **LLM prompt** | Any tier (e.g. `sonnet`) | Sends a prompt to Claude, same as sending a Telegram message |
| **Bash command** | `direct` | Runs a shell command inside the container, no LLM involved |

## Creating a job

Click **Add Job** and fill in the form:

| Field | Required | Description |
|-------|:--------:|-------------|
| **Name** | Yes | A short label for the job. Can't be changed after creation. |
| **Schedule** | Yes | Cron expression with seconds (see below). |
| **Tier** | No | Which model tier to use. Leave empty for the default. Set to `direct` for bash commands. |
| **Prompt** | Depends | The prompt to send to Claude. Shown when tier is not `direct`. |
| **Command** | Depends | The bash command to run. Shown when tier is `direct`. |
| **Output** | Yes | Where results go (see output options below). |
| **Skills** | No | Comma-separated skill names to activate for the job. |

### Cron format

ALF uses a 6-field cron expression (with seconds):

```
seconds  minutes  hours  day-of-month  month  day-of-week
```

| Schedule | Expression |
|----------|-----------|
| Every day at 9:30 AM | `0 30 9 * * *` |
| Every hour | `0 0 * * * *` |
| Every 15 minutes | `0 */15 * * * *` |
| Weekdays at 8 AM | `0 0 8 * * 1-5` |
| Every Monday at noon | `0 0 12 * * 1` |

### Output options

| Option | What happens |
|--------|-------------|
| `telegram` | Result is sent as a Telegram message |
| `file` | Result is saved to a file in the workspace |
| `both` | Telegram message + file |
| `silent` | Job runs but output is discarded |

## Filtering jobs

Use the filter bar at the top of the list:

| Filter | Shows |
|--------|-------|
| **All** | Every job |
| **Recurring** | Jobs that repeat (not one-shot) |
| **Today** | Jobs with next run within today |
| **This Week** | Jobs with next run within this week |
| **Later** | Jobs with next run beyond this week |

Jobs are sorted by next run time — soonest first.

## Editing and deleting

- Click **Edit** to change the schedule, tier, prompt, or output.
- Click **Delete** to remove the job permanently.
- Click **Disable** / **Enable** to pause a job without deleting it.

> Managed jobs (created by ALF itself) can only be enabled/disabled, not edited or deleted from the UI.

## Job types explained

### One-shot jobs

Jobs with the `auto_delete` flag run once and delete themselves. ALF creates these when you ask it to do something "in 5 minutes" or "tomorrow at 9 AM".

### Managed jobs

Jobs created by ALF during a conversation. These have a `managed` badge. You can enable/disable them but not edit them directly.

### System jobs

Internal jobs (like config watchers or update checks) are hidden from the UI. They run in the background and don't appear in the Schedules list.

## Examples

**Daily standup summary at 9 AM:**

| Field | Value |
|-------|-------|
| Name | `daily-standup` |
| Schedule | `0 0 9 * * *` |
| Tier | `sonnet` |
| Prompt | `Summarize what changed in the workspace since yesterday. List new files, modified files, and any open tasks.` |
| Output | `telegram` |

**Hourly git backup:**

| Field | Value |
|-------|-------|
| Name | `git-backup` |
| Schedule | `0 0 * * * *` |
| Tier | `direct` |
| Command | `cd /home/alf/data && git add -A && git commit -m "auto-backup $(date +%H:%M)" 2>/dev/null; true` |
| Output | `silent` |

## Common questions

**Can I schedule from Telegram?**
Yes. Just tell ALF: "schedule a daily check at 9am to review my tasks." ALF will create the job for you.

**What timezone are schedules in?**
The timezone configured in your `config.json` (`timezone` field). Defaults to the container's `TZ` environment variable, or UTC if neither is set.

**What if a job fails?**
The last error is shown in the job card. The job stays enabled and retries on the next scheduled run.

**Can I attach skills to a scheduled job?**
Yes. Enter skill names in the Skills field (comma-separated). The skills will be activated for that job's execution.

## What's next

- [Setting Up Tiers](tier-setup.md) — understand which tiers are available for scheduled jobs
- [Creating Skills](creating-skills.md) — create skills to use with scheduled prompts
