---
category: Operations
tags: logs, debugging, monitoring, errors
order: 7
---

# Logs

View ALF's daemon logs in real time from the Control Center.

## Quick start

1. Open the **Logs** tab in the sidebar.
2. Pick a log file from the dropdown.
3. Logs stream automatically. Use the search bar to filter.

## The interface

The Logs view has three controls at the top:

| Control | What it does |
|---------|-------------|
| **Log file dropdown** | Switch between available log files |
| **Lines** | How many lines to load (100, 200, 500, or 1000) |
| **Auto** checkbox | When checked, the log refreshes every 5 seconds |
| **Refresh** button | Manual refresh |

Below that is a **search bar** for filtering. Type any text to instantly filter visible lines.

## Reading the logs

Lines are color-coded by severity:

| Level | Color | Example |
|-------|-------|---------|
| ERROR | Red | `ERROR auth: invalid token` |
| WARN | Yellow | `WARNING: session timeout` |
| DEBUG | Dim | `DEBUG router: haiku → sonnet` |
| Info | Default | `router: hello → haiku (greeting)` |

The log auto-scrolls to the bottom so you always see the latest entries.

## What's in the logs

ALF logs everything about its operation:

- **Router decisions** - which tier was picked for each message and why
- **Provider calls** - Claude CLI invocations, timeouts, errors
- **Scheduler activity** - job starts, completions, failures
- **Agent orchestration** - task delegation, agent progress, results
- **Firewall events** - blocked requests (when in enforce mode)
- **Session management** - new sessions, expirations, evictions
- **Config changes** - tier reloads, hot-reload events

## Useful search patterns

| Search for | What you find |
|-----------|---------------|
| `router:` | All routing decisions |
| `ERROR` | All errors |
| `firewall` | Firewall proxy activity |
| `scheduler` | Scheduled job execution |
| `upgraded` | Write-intent tier upgrades |
| `timeout` | Timeout events |
| `orchestrator` or `agent` | Agent task coordination |

## Tool execution traces

Every LLM tool call is logged to `logs/traces/YYYY-MM-DD.jsonl` as a structured
span (`tool_exec`) with:

| Tag | Description |
|-----|-------------|
| `tool` | Tool name (e.g. `bash`, `read_file`, `write_file`) |
| `args` | JSON arguments, truncated to 500 chars. Sensitive keys (`token`, `key`, `secret`, `password`, `credential`, `auth`) are replaced with `[REDACTED]` |
| `exit_code` | Process exit code (0 = success, non-zero = failure, -1 = timeout or launch error) |
| `is_error` | `"true"` when the call failed |
| `error` | Short error description (stderr or timeout message), truncated to 500 chars |
| `output_len` | Response length in bytes |
| `duration_ms` | Execution time |

### Daily stats report

Once per day at 00:05 local time, the `tool-stats` system job aggregates the
last 7 days of traces into `logs/traces/stats-YYYY-MM-DD.json`:

```json
{
  "generated_at": "2026-04-16T00:05:00Z",
  "window_days": 7,
  "total_runs": 1459,
  "total_errors": 42,
  "tools": [
    {
      "tool": "bash",
      "runs": 412,
      "errors": 38,
      "err_rate": 0.092,
      "avg_ms": 280.4,
      "p95_ms": 1200,
      "max_ms": 4800,
      "last_error": "permission denied"
    },
    ...
  ]
}
```

Tools are sorted by error count (desc), then by run count (desc) — so the
noisiest/most-failing tools are at the top. Use this to spot flaky user tools
or slow native tools at a glance.

## Common questions

**Where are log files stored?**
Inside the container at the data directory. They persist across restarts as long as the data volume is mounted.

**Do logs rotate?**
Yes. ALF rotates log files automatically to prevent disk usage from growing indefinitely.

**Can I download logs?**
Not directly from the UI. Use the **Workspace** explorer on the Home tab to navigate to the log files and download them.

**The log viewer is slow with 1000 lines.**
Try reducing to 200 lines and using the search filter to find what you need. The filter runs client-side so it's instant.

## What's next

- [Firewall](firewall.md) - check the firewall request log for network-level debugging
- [Getting Started](getting-started.md) - overview of all ALF features
