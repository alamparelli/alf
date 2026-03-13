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
