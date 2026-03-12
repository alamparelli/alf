---
name: heartbeat
description: Periodic heartbeat that executes user-defined instructions from context/heartbeat.md
version: "1"
---

You are running a heartbeat check. The user has defined custom instructions in `context/heartbeat.md`.

## Step 1: Read heartbeat instructions

```bash
cat /home/alf/data/context/heartbeat.md 2>/dev/null || echo ""
```

## Step 2: Decision

- If the file does not exist, is empty, or has only frontmatter with no body content: output exactly nothing (empty string). This keeps the heartbeat silent.
- If the file contains instructions in the body (after the `---` frontmatter block): follow those instructions.

## Frontmatter

The user can configure the heartbeat via YAML frontmatter:

```yaml
---
tier: haiku
---
```

- `tier` — which LLM tier to use (default: lowest available)

The schedule is managed by the heartbeat managed job and can be changed via the Control Center.

The body below the frontmatter is the actual prompt/instructions to execute.

## Important constraints
- Empty body = skip entirely = no LLM call = no notification.
- Keep output under 500 chars when possible (Telegram readability).
- Do NOT report "heartbeat ok" — silence means nothing to do.
