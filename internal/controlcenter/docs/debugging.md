# Debugging

Reference for diagnosing issues in ALF. All paths are relative to the daemon's HOME (`/home/alf`).

---

## Log Format

**Telegram bot responses:**
```
2026/03/09 09:58:07 → claude-haiku-4-5 8809ms 1t $0.0749 sid:a1b2c3d4
```
Fields: `→ MODEL DURATIONms TURNSt $COST sid:SESSION_SHORT_ID`

**Agent (orchestrator) responses:**
```
2026/03/09 09:58:07 → agent 12000ms 3 iterations $0.2100 sid:task1234
```

**Control Center web chat:**
```
[chat-api] → claude-sonnet-4-6 5200ms 2t $0.0340 sid:e5f6g7h8
```

**Log levels:**
- `ERROR` - requires attention
- `WARN` - may need investigation
- `→` - response summary
- `[chat-api]` prefix - Control Center web chat
- No prefix - Telegram bot

---

## Filter by Session

Every log line includes `sid:XXXXXX`. Use it to track all activity for one conversation turn.

```bash
grep "sid:a1b2c3d4" logs/alf.log
```

In the CC Logs view, click the session chip to isolate.

## Filter by Model

Grep for the model name to see only responses from that model:

```bash
grep "claude-haiku-4-5" logs/alf.log
```

In CC Logs view, click model chips to filter.

---

## Session Files

Claude CLI sessions contain full conversation history, tool calls, and token usage.

| Source | Path |
|--------|------|
| Telegram | `~/.claude/projects/{chat_id}/sessions/{session_id}.json` |
| CC web | `~/.claude/projects/cc-{chat_id}/sessions/{session_id}.json` |

```bash
# Pretty-print a session
jq . ~/.claude/projects/12345/sessions/abc-def.json

# See tool calls in a session
jq '.messages[] | select(.tool_calls)' <session_file>
```

---

## Event Log

Structured events in `logs/events.jsonl`.

| Event Type | Description |
|------------|-------------|
| `message_in` / `message_out` | Incoming/outgoing messages with session_id, model, cost, tier |
| `session_new` | New session created (reason: `first` or `timeout`) |
| `agent_out` | Orchestrator task completed with iterations, cost, task_id |
| `bot_error` / `agent_error` | Errors with context |
| `route` | Routing decision with tier and reason |
| `schedule_run` | Scheduled job execution with job_id, job_name, tier, status (`ok`/`error`/`timeout`/`turn_limit`/`skipped`), cost, model |

```bash
# All events for a specific session
cat logs/events.jsonl | jq 'select(.data.session_id == "TARGET")'

# All errors
cat logs/events.jsonl | jq 'select(.type | endswith("_error"))'

# Routing decisions
cat logs/events.jsonl | jq 'select(.type == "route")'

# Cost summary by model
cat logs/events.jsonl | jq 'select(.type == "message_out") | {model: .data.model, cost: .data.cost}'
```

## Chat Message History

`logs/chat_messages.jsonl` stores all CC web chat messages with session IDs, model, tier, and cost.

```bash
cat logs/chat_messages.jsonl | jq 'select(.session_id == "TARGET")'
```

---

## Common Debug Workflows

### Slow response
```bash
# Check duration for a session
cat logs/events.jsonl | jq 'select(.type == "message_out" and .data.session_id == "TARGET") | .data.duration_ms'
```

### Wrong model used
```bash
# Check routing decision
cat logs/events.jsonl | jq 'select(.type == "route" and .data.session_id == "TARGET")'
```
Verify tier config in `config.d/tiers.json`.

### Session timeout
```bash
cat logs/events.jsonl | jq 'select(.type == "session_new" and .data.reason == "timeout")'
```
Check session timeout value in config.

### Turn limit reached
```bash
# Find all turn limit events
grep "turn limit" logs/alf.log

# Check which jobs hit the limit (from run log)
cat logs/events.jsonl | jq 'select(.data.status == "turn_limit") | {job: .data.job_name, tier: .data.tier, time: .ts}'

# Check scheduled job run history
cat logs/scheduler/*/*.txt 2>/dev/null | head -100
```
Common causes:
- Prompt too complex for the tier's `max_turns` setting
- Skill not providing enough structure - the model wastes turns figuring out what to do
- Missing tools - the model loops trying alternative approaches

Fix: simplify the prompt, increase `max_turns` in the tier config, or add targeted skills.

### Empty response
```bash
grep -E "suppressing empty response|Done \(no text output\)" logs/alf.log
```

### Provider startup failure
```bash
grep -E "preflight|heartbeat|binary check" logs/alf.log
```

### Permission errors
```bash
grep -iE "permission denied|EACCES" logs/alf.log
```

---

## Container Diagnostics

```bash
# Process overview
htop

# Daemon process info
cat /proc/1/status

# Claude CLI state
ls -la ~/.claude/

# Claude config
jq . ~/.claude.json

# Disk usage
df -h

# Session storage per chat
du -sh ~/.claude/projects/*/sessions/
```
