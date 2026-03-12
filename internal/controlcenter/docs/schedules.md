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

Three types of jobs:

| Type | Tier | What it does |
|------|------|-------------|
| **LLM prompt** | Any tier (e.g. `sonnet`) | Sends a prompt to Claude, same as sending a Telegram message |
| **Bash command** | `direct` | Runs a shell command inside the container, no LLM involved |
| **Reminder** | `reminder` | Sends a message directly to Telegram — no LLM, no bash, just a notification |

## Creating a job

Click **Add Job** and fill in the form:

| Field | Required | Description |
|-------|:--------:|-------------|
| **Name** | Yes | A short label for the job. Can't be changed after creation. |
| **Schedule** | Yes | Cron expression with seconds (see below). |
| **Tier** | No | Which model tier to use. Leave empty for the default. Set to `direct` for bash commands. |
| **Prompt** | Depends | The prompt to send to Claude. Shown when tier is not `direct`. |
| **Command** | Depends | The bash command to run. Shown when tier is `direct`. |
| **Message** | Depends | The text to send as a reminder. Mutually exclusive with prompt and command. |
| **Timeout** | No | Max execution time (e.g. `5m`, `30s`, `2h`). Default: 2 minutes for direct, 5 minutes for LLM. |
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
| **Managed** | Jobs created by ALF (health-check, heartbeat, etc.) |

Jobs are sorted by next run time — soonest first.

## Editing and deleting

- Click **Run** to trigger an immediate execution of the job. A confirmation popup appears before running. The job runs as a one-shot in the background — the original schedule is unaffected.
- Click **Edit** to change the schedule, tier, prompt, or output.
- Click **Delete** to remove the job permanently.
- Click **Disable** / **Enable** to pause a job without deleting it.

> Managed jobs (created by ALF itself) can only be enabled/disabled and manually triggered, not edited or deleted from the UI.

## Job types explained

### One-shot jobs

Jobs with the `auto_delete` flag run once and delete themselves. ALF creates these when you ask it to do something "in 5 minutes" or "tomorrow at 9 AM".

### Managed jobs

Built-in jobs bundled with ALF. These have a `managed` badge. You can change their **schedule**, **tier**, and **output** via the Settings button, and enable/disable them. You cannot edit their prompt or delete them.

Current managed jobs:

| Job | Schedule | What it does |
|-----|----------|-------------|
| **Health Check** | Every 2 hours | Runs system diagnostics (logs, disk, processes). Only invokes the LLM if errors are detected. |
| **Heartbeat** | Every 6 hours (default) | Reads `context/heartbeat.md`. Skips if the file body is empty. Executes the body as an LLM prompt when content is present. |

### Heartbeat

The heartbeat is a managed job that executes custom instructions you define in `context/heartbeat.md`. This lets you set up periodic checks without creating a full scheduled job.

**Setup:** Create or edit `context/heartbeat.md`:

```yaml
---
tier: haiku
---

Check if there are any pending tasks in my todo list and summarize them.
```

- **tier** — which model to use (optional, defaults to lowest available)
- **body** — the prompt to execute. Leave empty to skip.

The schedule is managed by the heartbeat managed job and can be changed via the Control Center Settings button.

The heartbeat file is preserved across upgrades — ALF never overwrites it.

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

## Execution logs

Every job execution is recorded with its status, duration, tier, and output. View logs:

- **Logs tab** → filter by `scheduler` to see execution entries
- **API** → `GET /api/schedule-logs?limit=50` returns recent executions as JSON

Logs include the job name, execution time, success/failure status, and output (truncated to 4KB per entry).

### Run statuses

| Status | Meaning |
|--------|---------|
| `ok` | Job completed successfully |
| `error` | Job failed (check `last_error` on the job card) |
| `timeout` | Job exceeded its timeout duration |
| `turn_limit` | Claude ran out of turns before completing (see below) |
| `skipped` | Job was still running from a previous trigger |

## Turn limit reached

When a scheduled job hits the turn limit, ALF sends a detailed notification to Telegram with:

- **Job name and ID** — which job failed
- **Tier** — which tier was used
- **Prompt snippet** — the beginning of the prompt that was sent

The job card also shows `turn limit reached` in the Last Error field.

### How to fix turn limit issues

1. **Simplify the prompt** — break complex instructions into smaller, focused steps. If a prompt asks to "research, analyze, and write a report", split it into separate jobs.
2. **Increase max_turns** — go to the Tiers tab, edit the tier used by this job, and increase the `Max turns` value.
3. **Switch tier** — use a tier with more turns or a more capable model.
4. **Use the `agent` tier** — for complex multi-step tasks, route to the orchestrator which handles iteration loops natively.
5. **Add skills** — providing structured skill prompts reduces wasted turns on figuring out how to do things.

## Daily digest

ALF can send a daily summary of all scheduled job executions. This is a system job that runs once per day and reports:

- How many jobs ran in the last 24 hours
- Success/failure counts
- Details of any failures

The digest is sent to Telegram automatically.

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
