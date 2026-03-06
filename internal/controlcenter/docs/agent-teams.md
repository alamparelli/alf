---
category: Advanced
tags: agents, orchestrator, teams, multi-agent, delegation
order: 15
---

# Agent Teams

ALF can coordinate multiple specialized AI agents to tackle complex tasks. An orchestrator (powered by Opus) breaks down your request, delegates sub-tasks to agents, reviews their results, and synthesizes a final answer.

Each agent works in an isolated session — it only sees the specific sub-task assigned to it. No context bleeds between agents.

## How to use it

### Force command

In Telegram or CC Chat, use `/orchestrator` followed by your request:

```
/orchestrator Research the latest Go 1.23 features and write a blog post about them
```

The orchestrator reads your message, decides which agents to involve, delegates work, and returns a combined result.

### Scheduled jobs

You can schedule orchestrator jobs using the schedule tool:

```bash
schedule create --name "weekly report" --schedule "0 0 9 * * 1" \
  --tier orchestrator --prompt "Analyze this week's git commits and write a summary report" \
  --output telegram
```

This runs every Monday at 9 AM, using the full agent team pipeline.

### When to use it

Use the orchestrator for:

- Complex tasks requiring multiple perspectives (research + writing + review)
- Tasks that benefit from specialization (one agent researches, another writes)
- Multi-step workflows where quality matters more than speed

> Don't use the orchestrator for simple questions or quick tasks. Regular tiers handle those faster and cheaper. The orchestrator adds overhead — it's worth it only when the task is complex enough to benefit from delegation.

## The starter team

ALF ships with a bundled starter team of 3 agents:

| Agent | What it does | Model |
|-------|-------------|-------|
| **researcher** | Gathers information, searches the web | Sonnet |
| **writer** | Drafts text, can write files | Sonnet |
| **reviewer** | Reviews quality, finds issues, suggests improvements | Sonnet |

The orchestrator (Opus) decides which agents to call, what to ask them, and whether the results are good enough — or if another round of delegation is needed.

## Creating custom teams

Teams live in `config.d/agents/` as JSON files. Each file defines one team.

Here's the full format:

```json
{
  "name": "my-team",
  "description": "What this team does",
  "max_agents_per_request": 3,
  "global_timeout_minutes": 15,
  "agents": [
    {
      "name": "agent-name",
      "description": "What this agent does",
      "model": "sonnet",
      "system_prompt": "You are a specialist in...",
      "tools": ["WebSearch"],
      "write_capable": false,
      "max_turns": 3,
      "effort": "medium"
    }
  ]
}
```

### Team fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Team name. Used as the `team/` prefix when the orchestrator delegates. |
| `description` | Yes | What the team does. The orchestrator reads this to decide when to use the team. |
| `max_agents_per_request` | No | Max agents running in parallel. Default: 3 |
| `global_timeout_minutes` | No | Max time for the entire orchestration. Default: 60 |

### Agent fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Agent identifier. Used in `team/agent` format when delegating. |
| `description` | Yes | What the agent does. The orchestrator reads this to decide who gets which task. |
| `model` | Yes | Which model to use: `haiku`, `sonnet`, or `opus`. |
| `system_prompt` | Yes | Instructions the agent follows. Be specific about what it should do and how. |
| `tools` | No | List of tools the agent can use (e.g. `WebSearch`, `Read`, `Edit`, `Write`, `Bash`). |
| `write_capable` | No | Can the agent create or modify files? Default: `false` |
| `max_turns` | No | Max conversation turns per delegation. Default: 3 |
| `effort` | No | Thinking effort: `low`, `medium`, `high`. Default: `medium` |

### Example: SEO content team

```json
{
  "name": "seo-content",
  "description": "SEO-optimized content creation and analysis",
  "max_agents_per_request": 3,
  "global_timeout_minutes": 20,
  "agents": [
    {
      "name": "keyword-researcher",
      "description": "Researches keywords, search volume, and competition",
      "model": "sonnet",
      "system_prompt": "You are an SEO keyword researcher. Find relevant keywords, analyze search intent, and suggest targeting opportunities.",
      "tools": ["WebSearch"],
      "max_turns": 5,
      "effort": "medium"
    },
    {
      "name": "content-writer",
      "description": "Writes SEO-optimized articles and blog posts",
      "model": "sonnet",
      "system_prompt": "You are an SEO content writer. Write engaging, well-structured content optimized for the given keywords. Use proper heading hierarchy, natural keyword placement, and clear meta descriptions.",
      "tools": ["Write"],
      "write_capable": true,
      "max_turns": 3,
      "effort": "medium"
    },
    {
      "name": "seo-auditor",
      "description": "Reviews content for SEO best practices",
      "model": "haiku",
      "system_prompt": "You are an SEO auditor. Check content for keyword density, heading structure, readability, internal linking opportunities, and meta tag quality. Be specific about what to fix.",
      "max_turns": 2,
      "effort": "low"
    }
  ]
}
```

## Ask ALF to help you create a team

Instead of writing JSON by hand, ask ALF to generate the configuration for you:

```
/sonnet_r Design an agent team for SEO content analysis with a keyword researcher, content writer, and SEO auditor. Show me the JSON.
```

ALF will generate the JSON. You can then save it via the **Workspace Explorer** in the Control Center: navigate to `config.d/agents/`, click the upload or create button, and paste the JSON.

> ALF cannot write directly to `config.d/` — it's a read-only directory for security. Use the Control Center to manage agent team files.

## Tips

- **Give agents clear, specific system prompts.** Vague prompts produce vague results. Tell each agent exactly what it should do and what format to use.
- **Use the cheapest model that can do the job.** Haiku for simple extraction or formatting. Sonnet for most tasks. Opus only when you truly need deep reasoning.
- **Keep `max_turns` low.** This controls cost. Start with 2-3 and increase only if agents need more steps.
- **The orchestrator can re-delegate.** If an agent's result is poor, the orchestrator sends it back with feedback. You don't need to handle retries yourself.
- **You can have multiple team files.** Drop as many JSON files into `config.d/agents/` as you want. The orchestrator sees all teams and picks the right agents for each task.

## How it works under the hood

1. The orchestrator receives your message along with ALF context and the full agent catalog.
2. It outputs a JSON delegation plan: `{"delegates": [{"agent": "team/agent", "task": "..."}]}`
3. ALF runs the delegated agents in parallel, each in an isolated working directory.
4. Agent results are sent back to the orchestrator.
5. The orchestrator either delegates more work or outputs the final answer: `{"response": "..."}`
6. This loop continues for up to 10 iterations (configurable via the `max_turns` field in the orchestrator tier).

The orchestrator runs **non-blocking** — you can continue chatting with ALF while it works in the background. Progress updates appear as animated status messages in Telegram.

### Orchestrator tier settings

The orchestrator tier in `tiers.json` controls how the orchestrator brain behaves:

| Field | Default | Description |
|-------|---------|-------------|
| `model` | `opus` | Model used for the orchestrator brain (reasoning and delegation). |
| `effort` | `high` | Thinking effort for the orchestrator brain. |
| `max_turns` | `1` | Max turns per orchestrator brain call. The brain outputs JSON — it doesn't use tools, so 1 is usually sufficient. Increase if you want the brain to use tools before delegating. |

The iteration limit (how many delegate→synthesize cycles) defaults to 10 and is bounded by the team's `global_timeout_minutes`.

> The entire flow is automatic. You send one message, and the orchestrator handles all the coordination. You only see the final synthesized answer.

## What's next?

- [Creating Skills](docs:creating-skills) — teach ALF new abilities with auto-injection
- [Setting Up Tiers](docs:tier-setup) — customize how ALF picks the right model
