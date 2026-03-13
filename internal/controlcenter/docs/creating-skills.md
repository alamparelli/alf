---
category: Development
tags: skills, triggers, auto-inject, SKILL.md, frontmatter, reference files, import, skills.sh
order: 11
---

# Creating Skills

Teach ALF new abilities by creating skills. A skill is a set of instructions that ALF follows when a topic comes up.

## Quick start

Create a folder in `~/data/skills/` with a `SKILL.md` file inside:

```bash
mkdir -p ~/data/skills/my-skill
cat > ~/data/skills/my-skill/SKILL.md << 'EOF'
---
name: my-skill
description: Summarizes meeting notes into action items
triggers: meeting, action items, recap
---

You are a meeting notes assistant. When the user shares meeting notes:

1. Extract all action items with owners
2. List key decisions made
3. Note any open questions
EOF
```

That's it. ALF picks up the skill automatically on next message.

## How skills work

Skills have two modes:

| Mode | How it works | When it happens |
|------|-------------|-----------------|
| **Catalog** | ALF sees the skill in its list and can choose to use it | Always (if `description` is set) |
| **Auto-inject** | The skill's full instructions are loaded automatically | When a trigger keyword matches the user's message |

Auto-injection is the powerful one. When you say "draft a tweet", ALF detects the trigger "tweet" and loads your X/Twitter skill instructions before responding.

## Skill file structure

```
skills/
  my-skill/
    SKILL.md              # Required - main instructions
    reference-data.md     # Optional - extra context (auto-loaded)
    style-guide.md        # Optional - more reference material
```

- `SKILL.md` is required. Everything else is optional.
- Any `.md` file in the same folder (except `SKILL.md`) is treated as reference material and appended to the prompt.
- Non-`.md` files (scripts, data) are ignored by the loader but ALF can still read them during a conversation.

## The SKILL.md format

Every `SKILL.md` starts with a **frontmatter** block (metadata between `---` lines), followed by the **body** (instructions).

```markdown
---
name: x-manager
description: X/Twitter content manager. Draft and publish tweets.
triggers: tweet, draft, twitter, x post, publication
tier: sonnet
version: "1"
---

You are an X/Twitter content manager for @username.

## How to draft a tweet
1. Read the style profile in `references/STYLE_PROFILE.md`
2. Write 3 variations
3. Ask the user to pick one
```

### Frontmatter fields

| Field | Required | What it does |
|-------|----------|-------------|
| `name` | No | Skill name. Defaults to the folder name if not set. |
| `description` | Yes* | One-line summary shown in the skill catalog. *Without this, ALF can't discover the skill.* |
| `triggers` | No | Comma-separated keywords for auto-injection (see below). |
| `tier` | No | Minimum tier name (e.g. `sonnet`). When this skill is active, ALF won't use a lower tier. |
| `version` | No | Version string, for your own tracking. |

### Trigger keywords

Triggers tell ALF when to automatically load your skill. When a user's message contains any trigger keyword, the full skill prompt is injected into the conversation.

```yaml
triggers: tweet, draft, twitter, x post, publication
```

Rules:
- Case-insensitive - "Tweet" and "tweet" both match
- Matched anywhere in the message - "can you draft something?" matches "draft"
- Use specific words to avoid false positives - "x" alone would match too many messages
- Multiple triggers are OR-based - any one match is enough

> Pick triggers carefully. Too generic (like single letters) causes the skill to load when it shouldn't. Too specific and it never loads. Aim for 3-6 triggers that clearly relate to the skill's purpose.

### Forcing a minimum tier

Some skills need a capable model. A skill that calls APIs, writes structured content, or runs multi-step workflows won't work well with Haiku.

Add `tier` to your frontmatter to set a floor:

```yaml
tier: sonnet
```

When this skill is active in the session, ALF will never use a tier with lower priority. If the router picks `haiku` but the skill requires `sonnet`, ALF automatically upgrades.

The tier name must match exactly one of your configured tiers in `tiers.json`.

> Use write-capable tiers for skills that need to run scripts, call APIs, or create files. Read-only tiers can't execute tools.

## Keeping skills under the size limit

ALF loads the entire skill prompt into the conversation context. Large prompts waste tokens and slow down responses.

**Soft limit: 8KB** for the total prompt (SKILL.md body + all reference files combined).

If your skill exceeds 8KB, you'll see this warning in logs:

```
skills: my-skill prompt is 14267B (soft limit 8192B)
```

The skill still works - it's a warning, not an error. But you should optimize it.

### How to split large skills

Instead of putting everything in `SKILL.md`, tell ALF to **read reference files on demand**:

**Before (everything in SKILL.md - 14KB):**
```markdown
---
name: x-manager
description: Draft and publish tweets
triggers: tweet, draft
---

You are a tweet writer.

## Style profile
[... 5KB of style analysis ...]

## Copywriting guide
[... 6KB of frameworks and techniques ...]
```

**After (SKILL.md is 1KB, references loaded on demand):**
```markdown
---
name: x-manager
description: Draft and publish tweets
triggers: tweet, draft
---

You are a tweet writer.

**Before drafting any tweet, read these reference files:**
- `references/STYLE_PROFILE.md` - personal voice analysis
- `references/copywriting-guide.md` - hooks, CTA, frameworks
```

Then create the reference files:
```
skills/
  x-manager/
    SKILL.md                          # Small - just instructions
    references/
      STYLE_PROFILE.md               # ALF reads this when needed
      copywriting-guide.md           # ALF reads this when needed
```

> Files inside subdirectories (like `references/`) are NOT auto-loaded into the prompt. Only `.md` files at the skill's root level are auto-loaded. Use subdirectories to keep large reference material out of the prompt.

### What counts toward the 8KB limit

| Counts | Doesn't count |
|--------|--------------|
| SKILL.md body (after frontmatter) | Frontmatter fields |
| Root-level `.md` files (auto-loaded) | Files in subdirectories |
| | Non-`.md` files (scripts, data) |

## Examples

### Simple skill - code reviewer

```markdown
---
name: code-review
description: Reviews code for bugs, security issues, and best practices
triggers: review, code review, audit code
---

You are a senior code reviewer. When asked to review code:

1. Check for bugs and logic errors
2. Flag security vulnerabilities (OWASP Top 10)
3. Suggest performance improvements
4. Keep feedback actionable - say what to fix, not just what's wrong
```

### Skill with API access

```markdown
---
name: weather
description: Gets weather forecasts using wttr.in
triggers: weather, forecast, temperature
---

You can check the weather using this command:

```bash
curl -s "wttr.in/{city}?format=j1"
```

When asked about weather:
1. Run the curl command with the requested city
2. Parse the JSON response
3. Summarize: temperature, conditions, and forecast
```

### Skill with reference files

```
skills/
  writing-assistant/
    SKILL.md
    tone-guide.md          # Auto-loaded (root level .md)
    references/
      brand-voice.md       # NOT auto-loaded (in subdirectory)
      examples.md          # NOT auto-loaded (in subdirectory)
```

## Importing skills from skills.sh

[skills.sh](https://skills.sh) is a community marketplace of ready-made skills. You can import any skill directly into ALF from the Control Center.

### How to import

1. Browse [skills.sh](https://skills.sh) and find a skill you want
2. Copy the install command (e.g. `npx skills add vercel-labs/skills --skill find-skills`)
3. In the Control Center, click **Import Skill** in the Workspace header
4. Paste the command, select a backend/model for the security scan
5. Click **Scan & Import**
6. Review the results:
   - **Verdict**: PASS (safe), WARN (review issues), or FAIL (security concerns)
   - **Issues**: any problems found by the security scanner
   - **Triggers**: suggested keywords (editable before install)
   - **Content**: full SKILL.md preview
7. Adjust triggers and tier if needed, then click **Install**

The skill is saved to `~/data/skills/{name}/SKILL.md` and reloaded automatically.

### Command formats

All of these work in the import dialog:

```
npx skills add owner/repo --skill skill-name
owner/repo --skill skill-name
owner/repo
```

If `--skill` is omitted, the repo name is used as the skill name.

### Security scanning

Every imported skill is analyzed by an LLM before installation. The scan checks for:

- **Prompt injection** - attempts to override system behavior
- **Secret access** - reading credentials or environment variables
- **Data exfiltration** - sending data to external services
- **Privilege escalation** - requesting elevated permissions
- **Destructive operations** - deleting files or modifying system config

You can still install skills with WARN or FAIL verdicts, but review the issues carefully first.

### Where imported skills live

Imported skills go to `~/data/skills/` (the user skills directory), not `skills.d/` (bundled). They include a `source` field in the frontmatter tracking the original GitHub repo:

```yaml
---
name: find-skills
description: Helps discover relevant skills
triggers: [find, discover, search skills]
tier: smart
source: vercel-labs/skills
---
```

## Overriding bundled skills

ALF loads skills in this order:

1. System skills (`/opt/alf/skills.d/`) - bundled with ALF
2. Bundled copies (`~/data/skills.d/`) - read-only
3. User skills (`~/data/skills/`) - your custom skills

If you create a skill with the same name as a bundled one, **yours completely replaces it**. This is a full override, not a merge — the bundled skill's instructions, triggers, tier, and reference files are all discarded. Only your version exists in memory.

To customize a bundled skill, duplicate it from the Workspace view in the Control Center first, then edit your copy. This preserves the original instructions as a starting point.

## Common questions

**My skill isn't being auto-injected. What's wrong?**
Check three things:
1. Does the skill have `triggers` in the frontmatter?
2. Does your message contain one of the trigger keywords?
3. Check logs for `skills: loaded N skills` - is your skill in the count?

**Can I use scripts or tools inside a skill?**
Yes. Put scripts in the skill folder and reference them by path. ALF can run them with bash during a conversation.

**Do skills work with scheduled jobs?**
Yes. When creating a scheduled job, use `--skills my-skill` to inject a skill into the job's prompt.

**How do I reload skills after editing?**
Skills reload automatically when ALF detects changes in the Workspace. You can also edit via the Control Center - save triggers a reload.

## What's next?

- [Building Tools & Extensions](docs:container-packages) - create tools, apps, and more
- [Tools Reference](docs:tools-reference) - built-in CLI tools
