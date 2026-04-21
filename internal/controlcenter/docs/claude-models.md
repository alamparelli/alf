---
category: Configuration
tags: claude, models, tiers, cli, dropdown, allowlist
order: 3
---

# Managing Claude Models

Add new Claude Code model IDs to ALF without waiting for a daemon update.

## Why this exists

When Anthropic ships a new Claude model, you don't have to wait for an ALF release to start using it. ALF keeps a **user-editable list** of Claude model identifiers that feeds the tier-form dropdown in the Control Center and the validator that accepts tier configs.

You can also add gateway-routed model IDs (for example `my-gw/claude-opus-4-7`) that don't match the standard naming scheme.

## Where the file lives

The file is created on first run at:

```
<configDir>/claude_models.txt
```

Typical paths:
- Docker: `/opt/alf/config/claude_models.txt`
- Local dev: `~/.config/alf/claude_models.txt` (or wherever your `configDir` is set)

> Changes take effect immediately — no restart needed. The file watcher picks up edits within ~2 seconds.

## Format

One model identifier per line. Lines that start with `#` (after trimming) are comments. Blank lines are ignored.

Example:

```
# Current
claude-opus-4-7
claude-sonnet-4-6
claude-haiku-4-5

# 1M context variants
claude-opus-4-7[1m]
claude-sonnet-4-6[1m]

# Gateway-routed custom deployment
my-gw/claude-opus-v1
```

Duplicate entries are silently deduplicated.

## Adding a new model

Let's say Anthropic ships `claude-sonnet-4-7`. You have three ways to use it:

**1. Just type it.** ALF accepts any model ID that starts with `claude-` out of the box, via the daemon's resolver pass-through. So you can put `"model": "claude-sonnet-4-7"` in a tier and it will work. It just won't appear in the dropdown.

**2. Add it to `claude_models.txt`.** Opens the dropdown so your team sees it. Edit the file, save, done:

```
claude-sonnet-4-7
claude-sonnet-4-7[1m]
```

**3. Use the Workspace editor.** Control Center → Workspace → navigate to `config.d/claude_models.txt` → edit.

## Short aliases (`haiku`, `sonnet`, `opus`)

Short aliases are **not** listed in `claude_models.txt` on purpose. They are handled by the daemon's internal resolver (`internal/ai/resolve.go`), which maps them to specific model versions:

| Alias | Resolves to |
|-------|-------------|
| `haiku` | `claude-haiku-4-5` |
| `sonnet` | `claude-sonnet-4-6` |
| `opus` | `claude-opus-4-6` |
| `sonnet-max` | `claude-sonnet-4-6-max` |
| `opus-max` | `claude-opus-4-6-max` |

These are always valid in a tier config — they don't need to be in the file. Use them when you want "the latest of this family" without pinning to a specific version.

> When a new generation ships, ALF updates the resolver mappings in a daemon release. The file exists specifically to let you move faster than that cycle.

## Validation rules

For tiers with backend `cli` (the default), the daemon accepts a `model` value if:

1. It's a known short alias (see table above), **or**
2. It starts with `claude-` (pass-through), **or**
3. It's listed in `claude_models.txt`

Anything else is rejected with `invalid model for tier <name>: <model>`.

API backends (`openrouter`, `openai`, etc.) skip this validation — any model ID is accepted because each provider has its own catalogue.

## Dropdown behaviour in the Control Center

In **Tiers → Add Tier** or **Edit Tier**, when the backend is `cli` (Claude Code):

- The **Model** field shows a dropdown of entries from `claude_models.txt`.
- You can also **type a free model ID** to add one on the fly. This is the same combobox pattern you'll see in well-made design tools — pick from the list, or type your own.
- If you type a new model and save, the form validates it against rules 1–3 above.

## Resetting to defaults

If you want to throw away your customisations and get ALF's shipped list back, delete the file:

```bash
rm ~/.config/alf/claude_models.txt
```

On the next daemon boot (or within 2 seconds via hot-reload), ALF re-seeds the file from the embedded default.

## Programmatic access

The CC exposes the current list via:

```
GET /api/models/claude
→ 200 {"models": ["claude-opus-4-7", ...]}
```

Subscribe to SSE event `claude_models` on `/api/events` to be notified when the list changes.

## Related

- [Setting Up Tiers](docs:tier-setup) — the `model` field in a tier references this list.
- [Backends](docs:backends) — API backends skip this validation; only `cli`-backend tiers use it.
