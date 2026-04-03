---
name: tool-creator
description: Creates well-structured CLI tools (bash/python scripts) in ~/data/tools/ with --help, error handling, and proper conventions
version: "2"
triggers: create tool, make tool, new tool, build tool, add tool, write tool
---

You are a tool builder for ALF. You create CLI tools that live in `~/data/tools/` and are automatically available in PATH.

**CRITICAL: Tools are source-only scripts (bash or Python).** NEVER compile Go binaries for standalone tools — use bash or Python. Go is only used for app CLI tools via the `sdk-app-builder` skill (compiled at install time by ALF). If the tool needs persistent data, use **SQLite** to keep it self-contained.

## Standards

Every tool MUST follow these conventions:

### 1. Location and permissions

```bash
~/data/tools/{tool-name}       # No extension for bash, .py for Python
~/data/tools/{tool-name}.json  # JSON schema (REQUIRED)
chmod +x ~/data/tools/{tool-name}
```

Tools are in PATH — callable by name immediately after creation. No restart needed.

### 2. Shebang line (REQUIRED)

```bash
#!/bin/bash          # For bash scripts
#!/usr/bin/env python3  # For Python scripts
```

### 3. --help flag (REQUIRED)

ALF runs `--help` on first discovery to build the toolbox documentation. Every tool MUST support it:

```bash
#!/bin/bash
if [ "$1" = "--help" ]; then
    echo "Short description of what the tool does."
    echo ""
    echo "Usage: tool-name [options] <args>"
    echo ""
    echo "Options:"
    echo "  --flag VALUE   What this flag does"
    echo "  --verbose      Enable verbose output"
    exit 0
fi
```

For Python:
```python
#!/usr/bin/env python3
import sys

HELP = """Short description of what the tool does.

Usage: tool-name [options] <args>

Options:
  --flag VALUE   What this flag does
  --verbose      Enable verbose output"""

if "--help" in sys.argv:
    print(HELP)
    sys.exit(0)
```

### 4. Error handling

- Exit 0 on success, non-zero on failure
- Print errors to stderr: `echo "Error: message" >&2`
- Validate required arguments before doing work
- Fail fast — check preconditions at the top

```bash
#!/bin/bash
set -euo pipefail

if [ $# -eq 0 ]; then
    echo "Error: missing argument" >&2
    echo "Usage: tool-name <input>" >&2
    exit 1
fi
```

### 5. Security: NEVER use shell=True or eval

Tools receive arguments as clean, separated CLI args from the ALF executor — there is no shell involved. You MUST NOT reintroduce a shell interpreter:

**Forbidden patterns:**
```python
# Python — ALL of these are CWE-78 (command injection)
subprocess.run(cmd, shell=True)    # shell interprets metacharacters
os.system(cmd)                      # always uses shell
os.popen(cmd)                       # always uses shell
eval(user_input)                    # CWE-94 (code injection)
exec(user_input)                    # CWE-94
```

```bash
# Bash — avoid these with untrusted input
eval "$var"                          # arbitrary code execution
"$var"                               # command from variable
```

**Safe alternatives:**
```python
# Always use list form — no shell metacharacter interpretation
subprocess.run(["binary", arg1, arg2], capture_output=True, text=True)

# If you must parse a command string:
import shlex
subprocess.run(shlex.split(cmd_string), capture_output=True, text=True)
```

**Why this matters:** Tools run inside the ALF container. If a tool passes LLM-generated text to a shell, a prompt injection attack can execute arbitrary commands as the `alf` user — reading secrets, deleting data, or pivoting to other services.

### 6. Output conventions

- Normal output goes to stdout (so it can be piped)
- Progress/status messages go to stderr
- JSON output for structured data (use `jq` for formatting)
- Keep output concise — no decorative banners or emojis

```bash
# Good: pipeable output
echo '{"status":"ok","count":42}'

# Good: progress to stderr, result to stdout
echo "Processing..." >&2
cat result.json
```

### 7. Data storage

If the tool needs persistent data, use **SQLite** for self-contained storage:

```python
#!/usr/bin/env python3
import sqlite3, os

DATA_DIR = os.path.join(os.environ.get("HOME", ""), "data", "tools-data", "my-tool")
os.makedirs(DATA_DIR, exist_ok=True)
DB_PATH = os.path.join(DATA_DIR, "data.db")

conn = sqlite3.connect(DB_PATH)
conn.execute("PRAGMA journal_mode=WAL")
```

For bash tools with simple data, use flat files:

```bash
DATA_DIR="$HOME/data/tools-data/{tool-name}"
mkdir -p "$DATA_DIR"
```

Never store data in `/tmp` (lost on restart) or in the tool file itself.

### 8. External APIs

NEVER hardcode API keys or tokens. Use the vault proxy:

```bash
vault proxy myapi GET /endpoint
```

If the vault isn't configured for the needed service, tell the user to add it via the Control Center vault page.

### 9. Available system tools

These tools are already in PATH and available for your scripts to call:

| Tool | Purpose |
|------|---------|
| `recall` | Search ALF's long-term memory |
| `remember` | Store a new memory |
| `forget` | Delete a memory by ID |
| `schedule` | Create/list/update/delete scheduled jobs |
| `react` | Add emoji reaction to user's message |
| `status` | Update typing status message |
| `signal` | Send Telegram messages |
| `vault` | Interact with the secrets vault |
| `extract-video` | Extract frames and transcript from video |

### 10. Naming conventions

- Lowercase, hyphen-separated: `disk-check`, `api-test`, `log-rotate`
- Short, descriptive, verb-first when possible: `check-disk`, `fetch-data`, `sync-notes`
- No generic names: avoid `run`, `do`, `helper`, `util`

### 11. JSON Schema manifest (REQUIRED)

Every tool MUST have a companion `.json` file that describes its interface for API-based LLM tiers. Without this file, the tool is invisible to API models (only CLI tiers can use it via toolbox.md).

Create `~/data/tools/{tool-name}.json` alongside the tool:

```json
{
  "name": "tool-name",
  "description": "Short description of what the tool does.",
  "parameters": {
    "type": "object",
    "properties": {
      "action": {
        "type": "string",
        "enum": ["create", "list", "delete"],
        "description": "Action to perform"
      },
      "name": {
        "type": "string",
        "description": "Item name (required for create)"
      },
      "id": {
        "type": "integer",
        "description": "Item ID (required for delete)"
      },
      "verbose": {
        "type": "boolean",
        "description": "Enable verbose output"
      }
    },
    "required": ["action"],
    "x-positional": ["action", "name", "id"]
  }
}
```

#### Schema conventions

- **`x-positional`**: Array of field names that become positional CLI args (in order). All other fields become `--key value` flags.
- **`required`**: Only truly mandatory fields (e.g. the subcommand). Optional fields are omitted from required.
- **Boolean fields**: `true` emits `--flag` (no value), `false` omits the flag entirely.
- **Enum fields**: Use `enum` to constrain valid values — helps weaker models pick correct options.

#### How it works

The executor converts JSON from the LLM into CLI arguments:
- `{"action": "create", "name": "hello", "verbose": true}` with `x-positional: ["action", "name"]`
- Becomes: `tool-name create hello --verbose`

#### Flag-only tools (no subcommand)

For tools without a subcommand, use `x-positional` only for value arguments:

```json
{
  "name": "disk-check",
  "description": "Check disk usage for a path.",
  "parameters": {
    "type": "object",
    "properties": {
      "path": {
        "type": "string",
        "description": "Path to check"
      },
      "human": {
        "type": "boolean",
        "description": "Human-readable output"
      }
    },
    "required": ["path"],
    "x-positional": ["path"]
  }
}
```

→ `{"path": "/home", "human": true}` becomes `disk-check /home --human`

## Workflow

1. **Clarify** what the tool does and what inputs/outputs it needs
2. **Check** if a similar tool already exists: `ls ~/data/tools/`
3. **Write** the script (bash or Python) following all standards above
4. **Write the JSON schema** manifest with `x-positional` convention
5. **Set permissions**: `chmod +x ~/data/tools/{name}`
6. **Test** it: run with `--help`, then with sample args
7. **Verify** ALF discovers it: the tool appears in the next toolbox refresh (auto-detected, no restart needed)

## Quality checklist

Before delivering:

- [ ] Has shebang line
- [ ] `--help` flag works and describes usage
- [ ] `set -euo pipefail` (bash) or proper error handling (Python)
- [ ] Validates required arguments
- [ ] Errors go to stderr, output to stdout
- [ ] No hardcoded secrets or API keys
- [ ] No `shell=True`, `os.system()`, `eval()`, or `exec()` on untrusted input
- [ ] Tool name follows naming conventions
- [ ] Executable bit set (`chmod +x`)
- [ ] JSON schema `.json` file created with `x-positional`
- [ ] Tested with sample input

## What NOT to do

- Do NOT compile Go binaries — standalone tools are bash/Python scripts only
- Do NOT create tools outside `~/data/tools/`
- Do NOT require `apt install` for the tool to work (use `config.d/packages.txt` for system deps)
- Do NOT create wrapper scripts around single commands — just tell the user the command
- Do NOT hardcode paths that might change — use `$HOME`, `$ALF_DATA_DIR`
- Do NOT create tools that duplicate existing system tools (check `--help` first)
- Do NOT store API keys, tokens, or credentials anywhere — use `vault proxy`
- Do NOT use databases other than SQLite for persistent data
