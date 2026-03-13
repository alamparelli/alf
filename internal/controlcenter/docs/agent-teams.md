---
category: Advanced
tags: agents, agent, teams, multi-agent, delegation
order: 15
---

# Agent Teams

ALF can coordinate multiple specialized AI agents to tackle complex tasks. An agent (powered by Opus) breaks down your request, delegates sub-tasks to agents, reviews their results, and synthesizes a final answer.

Each agent works in an isolated session — it only sees the specific sub-task assigned to it. No context bleeds between agents.

## How to use it

### Automatic routing

When the agent tier is enabled, the router automatically detects multi-step tasks and routes them to the agent. Phrases like "use agents", "lance une équipe", "coordinate multiple tasks", or complex research+write+review workflows trigger agent routing.

### Force command

In Telegram or CC Chat, use `/agent` followed by your request:

```
/agent Research the latest Go 1.23 features and write a blog post about them
```

The agent reads your message, decides which agents to involve, delegates work, and returns a combined result.

### Monitoring tasks

Open the **Tasks** tab in the Control Center to see running and completed agent tasks. Each card shows the prompt, number of iterations, agent calls, cost, and elapsed time. Click a card to expand and see individual agent results.

### Scheduled jobs

You can schedule agent jobs using the schedule tool:

```bash
schedule create --name "weekly report" --schedule "0 0 9 * * 1" \
  --tier agent --prompt "Analyze this week's git commits and write a summary report" \
  --output telegram
```

This runs every Monday at 9 AM, using the full agent team pipeline.

### When to use it

Use the agent for:

- Complex tasks requiring multiple perspectives (research + writing + review)
- Tasks that benefit from specialization (one agent researches, another writes)
- Multi-step workflows where quality matters more than speed

> Don't use the agent for simple questions or quick tasks. Regular tiers handle those faster and cheaper. The agent adds overhead — it's worth it only when the task is complex enough to benefit from delegation.

## The starter team

ALF ships with a bundled starter team of 3 agents. Each agent references a tier from your `tiers.json`:

| Agent | What it does | Tier |
|-------|-------------|------|
| **researcher** | Gathers information, searches the web, reads files | sonnet |
| **writer** | Drafts text, writes files, creates content | sonnet |
| **reviewer** | Reviews quality, finds issues, suggests improvements | sonnet |

The tier defines the model, tools, write permissions, max turns, and effort level. You can customize agent capabilities by creating dedicated tiers (e.g. a "researcher" tier with web search tools and 15 turns).

The agent (Opus) decides which agents to call, what to ask them, and whether the results are good enough — or if another round of delegation is needed.

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
      "tier": "sonnet",
      "system_prompt": "You are a specialist in..."
    }
  ]
}
```

Each agent references a tier from `tiers.json`. The tier defines the model, tools, write permissions, effort level, and max turns. This means you can reuse existing tiers across agents and change execution parameters in one place.

### Team fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Team name. Used as the `team/` prefix when the agent delegates. |
| `description` | Yes | What the team does. The agent reads this to decide when to use the team. |
| `max_agents_per_request` | No | Max agents running in parallel. Default: 3 |
| `global_timeout_minutes` | No | Max time for the entire agent run. Default: 60 |

### Agent fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Agent identifier. Used in `team/agent` format when delegating. |
| `description` | Yes | What the agent does. The agent reads this to decide who gets which task. |
| `tier` | Yes | Which tier to use (from `tiers.json`). The tier defines model, tools, effort, max_turns, and write permissions. |
| `system_prompt` | Yes | Instructions the agent follows. Be specific about what it should do and how. Combined with the tier's system prompt if any. |

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
      "tier": "sonnet",
      "system_prompt": "You are an SEO keyword researcher. Find relevant keywords, analyze search intent, and suggest targeting opportunities."
    },
    {
      "name": "content-writer",
      "description": "Writes SEO-optimized articles and blog posts",
      "tier": "sonnet",
      "system_prompt": "You are an SEO content writer. Write engaging, well-structured content optimized for the given keywords. Use proper heading hierarchy, natural keyword placement, and clear meta descriptions."
    },
    {
      "name": "seo-auditor",
      "description": "Reviews content for SEO best practices",
      "tier": "haiku",
      "system_prompt": "You are an SEO auditor. Check content for keyword density, heading structure, readability, internal linking opportunities, and meta tag quality. Be specific about what to fix."
    }
  ]
}
```

## Ask ALF to help you create a team

Instead of writing JSON by hand, ask ALF to generate the configuration for you:

```
/sonnet Design an agent team for SEO content analysis with a keyword researcher, content writer, and SEO auditor. Show me the JSON.
```

ALF will generate the JSON. You can then save it via the **Workspace Explorer** in the Control Center: navigate to `config.d/agents/`, click the upload or create button, and paste the JSON.

> ALF cannot write directly to `config.d/` — it's a read-only directory for security. Use the Control Center to manage agent team files.

## Tips

- **Give agents clear, specific system prompts.** Vague prompts produce vague results. Tell each agent exactly what it should do and what format to use.
- **Create tiers for different agent roles.** A researcher tier with web search tools and 15 turns, a writer tier with write permissions, a reviewer tier with haiku and few turns.
- **Give agents enough turns (via the tier).** Start with 10-15 for agents that use tools. Use 5-10 for review-only agents. Too few turns → "turn limit reached" errors.
- **The agent can re-delegate.** If an agent's result is poor, the agent sends it back with feedback. You don't need to handle retries yourself.
- **You can have multiple team files.** Drop as many JSON files into `config.d/agents/` as you want. The agent sees all teams and picks the right agents for each task.

## How it works under the hood

1. The agent receives your message along with ALF context and the full agent catalog.
2. It outputs a JSON delegation plan: `{"delegates": [{"agent": "team/agent", "task": "..."}]}`
3. ALF runs the delegated agents in parallel, each in an isolated working directory.
4. Agent results are sent back to the agent.
5. The agent either delegates more work or outputs the final answer: `{"response": "..."}`
6. This loop continues for up to 10 iterations (configurable via the `max_turns` field in the agent tier).

The agent runs **non-blocking** — you can continue chatting with ALF while it works in the background. Progress updates appear as animated status messages in Telegram.

### Agent tier settings

The agent tier in `tiers.json` controls how the agent brain behaves:

| Field | Default | Description |
|-------|---------|-------------|
| `model` | `opus` | Model used for the agent brain (reasoning and delegation). |
| `effort` | `medium` | Thinking effort for the agent brain. `medium` is recommended — `high` uses extended thinking which consumes more turns. |
| `max_turns` | `10` | Max turns per agent brain call. The brain outputs JSON text only (no tools). |
| `max_iterations` | `10` | How many delegate→synthesize cycles. Each cycle = one agent call + agent delegation. |
| `timeout_minutes` | `60` | Hard timeout for the entire agent run. Also bounded by each team's `global_timeout_minutes`. |

To enable the agent, set `"enabled": true` in `tiers.json`. The agent is disabled by default.

> The agent brain has no tools — it only outputs JSON text. If the brain repeatedly hits "turn limit reached", increase `max_turns`. If tasks time out before completing, increase `max_iterations` or `timeout_minutes`.

> The entire flow is automatic. You send one message, and the agent handles all the coordination. You only see the final synthesized answer.

## What's next?

- [Creating Skills](docs:creating-skills) — teach ALF new abilities with auto-injection
- [Setting Up Tiers](docs:tier-setup) — customize how ALF picks the right model
