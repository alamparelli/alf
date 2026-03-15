---
category: Operations
tags: debugging, logs, errors, troubleshooting, sessions
order: 8
---

# Troubleshooting

Quick fixes for common problems, plus advanced tools for deeper investigation.

## Start here

| Symptom | What to check |
|---------|--------------|
| ALF doesn't reply | **Logs** tab — look for ERROR entries |
| Wrong model used | **Logs** tab — search for `router:` to see routing decisions |
| Slow responses | **Logs** tab — check the response time (shown in milliseconds) |
| Scheduled job didn't run | **Schedules** tab — check the job's Last Error field |
| "Turn limit reached" | Edit the tier in **Tiers** tab and increase Max Turns, or simplify the prompt |

Most problems show up in the **Logs** tab. Start there before going deeper.

---

## Reading the logs

Each response line looks like this:

```
→ claude-haiku-4-5 8809ms 1t $0.0749 sid:a1b2c3d4
```

| Part | Meaning |
|------|---------|
| `→` | Response summary |
| `claude-haiku-4-5` | Model used |
| `8809ms` | How long it took |
| `1t` | Number of tool turns |
| `$0.0749` | Cost |
| `sid:a1b2c3d4` | Session ID (use to filter related logs) |

Lines from the Control Center Chat are prefixed with `[chat-api]`.

### Useful search terms

| Search for | What you find |
|-----------|---------------|
| `ERROR` | All errors |
| `router:` | Routing decisions |
| `turn limit` | Jobs that ran out of steps |
| `timeout` | Timeout events |
| `scheduler` | Scheduled job execution |

---

## Common fixes

### Turn limit reached

The model ran out of steps before finishing. Common causes:
- Prompt too complex for the tier's `max_turns`
- Missing skill — the model wastes turns figuring things out
- Missing tools — the model loops trying alternatives

**Fix:** simplify the prompt, increase `max_turns` in the tier config, or add a targeted skill.

### Wrong model used

Check the **Logs** tab for `router:` entries to see why the router chose that tier. Then review your tier `router_label` descriptions — make them more specific.

### Scheduled job failed

Open the **Schedules** tab. The job card shows the last error. Common issues:
- Vault locked (if the job needs API access)
- Turn limit reached (increase `max_turns` or simplify the prompt)
- Timeout (increase the job's timeout value)

---

## Advanced: terminal debugging

For deeper investigation, use the **Terminal** tab.

### Event log

Structured events are stored in `logs/events/` with daily rotation (e.g., `logs/events/2026-03-15.jsonl`):

```bash
# All errors (today)
cat logs/events/$(date +%Y-%m-%d).jsonl | jq 'select(.type | endswith("_error"))'

# Routing decisions for a session
cat logs/events/*.jsonl | jq 'select(.type == "route" and .data.session_id == "TARGET")'

# Cost summary by model
cat logs/events/*.jsonl | jq 'select(.type == "message_out") | {model: .data.model, cost: .data.cost}'
```

### Container diagnostics

```bash
htop                              # Process overview
df -h                             # Disk usage
jq . ~/.claude.json               # Claude config
du -sh ~/.claude/projects/*/sessions/  # Session storage
```
