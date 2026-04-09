---
category: Reference
tags: error journal, tools, apps, heartbeat, self-repair, debugging
order: 23
---

# Error Journal

The error journal is a unified log of tool and app errors that enables autonomous self-repair via the heartbeat.

## How it works

When a user tool fails or an app reports an error, the event is recorded in `data/logs/error-journal.jsonl` with full context. The heartbeat reads unresolved errors and appends a summary to its prompt, giving the LLM enough context to diagnose and fix the issue autonomously.

### Flow

```
Tool/app fails → error logged with context → heartbeat sees summary → LLM fixes → tool succeeds → error auto-resolved
```

### What gets logged

Each error entry contains:

| Field | Description |
|-------|-------------|
| `kind` | `"tool"` or `"app"` |
| `tool` | Tool name or app slug |
| `args` | JSON arguments that caused the failure (tools only) |
| `error` | Error message (stderr output or JS error) |
| `stack` | Stack trace (apps only) |
| `source_hash` | SHA-256 of the tool source at the time of error |
| `timestamp` | When the error occurred |
| `resolved` | Whether the error has been auto-resolved |

### Auto-resolution

When a tool that previously failed executes successfully, all its unresolved errors are automatically marked as resolved. No manual intervention needed.

### Source hash tracking

The `source_hash` field records the tool's content hash at the time of error. When the heartbeat generates its summary, it compares the stored hash with the current file — if they differ, the summary notes that the source was modified and the error may already be fixed.

## Heartbeat integration

The heartbeat appends a grouped error summary to whatever instructions are already in `heartbeat.md`:

| heartbeat.md body | Unresolved errors | Result |
|---|---|---|
| Empty | None | Skip (no LLM call) |
| Empty | Yes | Run with error summary only |
| Has content | None | Run with content only |
| Has content | Yes | Run with content + errors appended |

The summary groups errors by tool/app and includes:
- Error count
- Last error message
- Failing arguments (tools) or stack trace (apps)
- Whether the source was modified since the error
- Fix instructions (file path to read)

## Storage

- **File**: `data/logs/error-journal.jsonl`
- **Format**: One JSON object per line (JSONL)
- **Retention**: Ring buffer, max 200 entries
- **Concurrency**: Mutex-protected, safe for concurrent access

## Error sources

### Tool errors

Captured automatically by the tool executor when a user tool (in `tools/` or `tools.d/`) fails:
- Execution failure (non-zero exit code)
- Timeout
- Stderr output

Native Go tools are not logged (they have different error handling).

### App errors

Captured via the existing `POST /api/apps/{slug}/errors` endpoint. When an app reports a JavaScript error, it's written to both the per-app `errors.json` (for the app's own error page) and the unified error journal (for heartbeat repair).

## API

No direct API endpoint for the error journal. Errors are read by the heartbeat internally. To inspect errors manually:

```bash
cat ~/data/logs/error-journal.jsonl | python3 -m json.tool
```
