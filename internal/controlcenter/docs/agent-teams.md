---
category: Advanced
tags: agents, agent, teams, multi-agent, delegation
order: 15
---

# Agent Teams

ALF can coordinate multiple specialized AI agents to tackle complex tasks. An orchestrator brain breaks down your request, delegates sub-tasks to specialized agents, reviews their results, and synthesizes a final answer.

The orchestrator brain runs on the tier named `agent` in your `tiers.json` - its model, effort, and turn limits are all configurable there. Each sub-agent runs on its own tier, with its own model, tools, and permissions. No context bleeds between agents.

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
  --output chat
```

This runs every Monday at 9 AM, using the full agent team pipeline.

### When to use it

Use the agent for:

- Complex tasks requiring multiple perspectives (research + writing + review)
- Tasks that benefit from specialization (one agent researches, another writes)
- Multi-step workflows where quality matters more than speed

> Don't use the agent for simple questions or quick tasks. Regular tiers handle those faster and cheaper. The agent adds overhead - it's worth it only when the task is complex enough to benefit from delegation.

## The starter team

ALF ships with a bundled starter team of 3 agents. Each agent references a tier from your `tiers.json`:

| Agent | What it does | Tier |
|-------|-------------|------|
| **researcher** | Gathers information, searches the web, reads files | sonnet |
| **writer** | Drafts text, writes files, creates content | sonnet |
| **reviewer** | Reviews quality, finds issues, suggests improvements | sonnet |

The tier defines the model, tools, write permissions, max turns, and effort level. You can customize agent capabilities by creating dedicated tiers (e.g. a "researcher" tier with web search tools and 15 turns).

The orchestrator brain decides which agents to call, what to ask them, and whether the results are good enough - or if another round of delegation is needed.

## Creating custom teams

Teams live in `config.d/agents/` as JSON files. Each file defines one team.

Here's the full format:

```json
{
  "name": "my-team",
  "description": "What this team does",
  "orchestrator_prompt": "Optional instructions for the orchestrator when using this team",
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
| `description` | Yes | What the team does. The orchestrator reads this to decide when to use the team. |
| `orchestrator_prompt` | No | Extra instructions injected into the orchestrator's system prompt for this team. Use this to guide how the orchestrator coordinates agents — e.g. enforcing workflows, quality gates, or output formats. |
| `max_agents_per_request` | No | Max agents running in parallel. Default: 3 |
| `global_timeout_minutes` | No | Max time for the entire agent run. Default: 60 |

### Agent fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Agent identifier. Used in `team/agent` format when delegating. |
| `description` | Yes | What the agent does. The agent reads this to decide who gets which task. |
| `tier` | Yes | Which tier to use (from `tiers.json`). The tier defines model, tools, effort, max_turns, and write permissions. |
| `system_prompt` | Yes | Instructions the agent follows. Be specific about what it should do and how. Combined with the tier's system prompt if any. |
| `skills` | No | List of skill names to inject into this agent (e.g. `["app-builder", "tool-creator"]`). When set, only these skills are injected — not the globally matched ones. When omitted, the agent receives all skills matched by the user's message (default behavior). |

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

### Example: marketplace app team with per-agent skills

```json
{
  "name": "app-factory",
  "description": "Build and publish ALF marketplace apps",
  "max_agents_per_request": 3,
  "global_timeout_minutes": 30,
  "agents": [
    {
      "name": "developer",
      "description": "Builds the app (server, frontend, manifest)",
      "tier": "sonnet",
      "system_prompt": "You build ALF marketplace apps. Follow the skill instructions exactly.",
      "skills": ["app-builder", "tool-creator"]
    },
    {
      "name": "reviewer",
      "description": "Reviews code quality and security",
      "tier": "haiku",
      "system_prompt": "Review the app code for bugs, security issues, and marketplace compliance."
    },
    {
      "name": "publisher",
      "description": "Publishes the app to the marketplace",
      "tier": "sonnet",
      "system_prompt": "Package and publish the app to the ALF marketplace.",
      "skills": ["marketplace-developer"]
    }
  ]
}
```

The `developer` agent always gets `app-builder` and `tool-creator` skills injected, regardless of what the user's message triggers. The `reviewer` has no explicit skills — it receives whatever skills match the user's message (default behavior).

## Ask ALF to help you create a team

Instead of writing JSON by hand, ask ALF to generate the configuration for you:

```
/sonnet Design an agent team for SEO content analysis with a keyword researcher, content writer, and SEO auditor. Show me the JSON.
```

ALF will generate the JSON. You can then save it via the **Workspace Explorer** in the Control Center: navigate to `config.d/agents/`, click the upload or create button, and paste the JSON.

> ALF cannot write directly to `config.d/` - it's a read-only directory for security. Use the Control Center to manage agent team files.

## Tips

- **Use `orchestrator_prompt` to steer coordination.** This field tells the orchestrator *how* to use the team — e.g. "always run the reviewer after the writer" or "output results in French". Unlike `description` (which is a short label), `orchestrator_prompt` can contain detailed workflow rules.
- **Give agents clear, specific system prompts.** Vague prompts produce vague results. Tell each agent exactly what it should do and what format to use.
- **Create dedicated tiers for agent roles.** A "researcher" tier with web search tools and 15 turns, a "writer" tier with write permissions, a "reviewer" tier on haiku with 5 turns. Multiple agents can share the same tier.
- **Give agents enough turns (via their tier).** Start with 10-15 for agents that use tools. Use 5-10 for review-only agents. Too few turns → "turn limit reached" errors.
- **Use tier system prompts for shared instructions.** If all agents in a role need the same base instructions, put them in the tier's `system_prompt`. The agent's own `system_prompt` is appended after.
- **The orchestrator can re-delegate.** If an agent's result is poor, the brain sends it back with feedback. You don't need to handle retries yourself.
- **You can have multiple team files.** Drop as many JSON files into `config.d/agents/` as you want. The orchestrator sees all teams and picks the right agents for each task.
- **Assign skills to agents.** Use the `skills` field to give an agent specific capabilities (e.g. `"skills": ["app-builder"]`). This injects the skill prompt into that agent only — other agents in the team won't receive it. Useful for specialized workflows.
- **API backends work too.** Set `backend` on a tier to route agents through an API provider (e.g. OpenRouter). This lets you mix CLI and API models in the same team.

## How it works under the hood

1. The orchestrator brain receives your message along with ALF context and the full agent catalog.
2. It outputs a JSON delegation plan: `{"delegates": [{"agent": "team/agent", "task": "..."}]}`
3. ALF runs the delegated agents in parallel, each in an isolated working directory under `data/agents/<taskID>/`.
4. Each sub-agent runs on **its own tier** - with its own model, tools, effort, and turn limits.
5. Agent results are sent back to the orchestrator brain.
6. The brain either delegates more work or outputs the final answer: `{"response": "..."}`
7. This loop continues for up to `max_iterations` cycles (default 20).

The orchestrator runs **non-blocking** - you can continue chatting with ALF while it works in the background. Progress updates appear as animated status messages in Telegram.

### Orchestrator tier settings

The `agent` tier in `tiers.json` controls the orchestrator brain:

| Field | Default | Description |
|-------|---------|-------------|
| `model` | `opus` | Model used for the orchestrator brain (reasoning and delegation). |
| `effort` | `medium` | Thinking effort for the brain. `medium` is recommended. |
| `orchestrator_max_turns` | `3` | Max turns per brain call. The brain outputs JSON text only (no tools), so few turns suffice. |
| `max_iterations` | `20` | How many delegate→synthesize cycles. Each cycle = one brain call + parallel agent delegation. |
| `timeout_minutes` | `60` | Hard timeout for the entire run. Also bounded by each team's `global_timeout_minutes`. |

### Sub-agent execution

Each sub-agent inherits execution parameters from **its own tier** (not the `agent` tier):

| Tier field | Effect on the sub-agent |
|------------|------------------------|
| `model` | Which model runs the agent (e.g. haiku, sonnet, opus). |
| `tools` | Which tools the agent can use. Use `["*"]` for all, `["*native"]` for native-only. |
| `max_turns` | How many turns the agent gets. 10-15 for tool-heavy agents, 5-10 for review-only. |
| `effort` | Thinking effort level. |
| `write_capable` | Whether the agent can write files. |
| `system_prompt` | Tier-level prompt, combined with the agent's `system_prompt`. Tier prompt comes first. |
| `backend` | Which LLM backend to use (empty = default CLI, or a configured API backend). |

To enable the orchestrator, set `"enabled": true` on the `agent` tier. It is disabled by default.

> The orchestrator brain has no tools - it only outputs JSON text. If the brain repeatedly hits "turn limit reached", increase `orchestrator_max_turns`. If tasks time out before completing, increase `max_iterations` or `timeout_minutes`.

> Sub-agent "turn limit reached" errors → increase `max_turns` on that agent's tier.

> The entire flow is automatic. You send one message, and the orchestrator handles all the coordination. You only see the final synthesized answer.

## Arbitrage mode

Agents can pause execution and ask the user questions using `[[QUESTION: ...]]` markers in their output. When the orchestrator detects these markers (or outputs questions itself via Option D), the task enters `awaiting_arbitration` status.

In the Tasks tab, arbitration tasks auto-expand and display the questions with a text area for your answers. Click **Submit** to resume the task with your input.

To use this in agent prompts, instruct agents to output markers when they need clarification:

```
If you are unsure about the user's preference, output: [[QUESTION: your question here]]
```

The orchestrator will detect these markers, pause execution, and relay the questions to the user.

## Task notifications

Tasks automatically report to the chat interface (CC Chat and system messages) when they:

- **Complete** — a summary of the result is sent as a system message
- **Fail** — failure notification with the task ID
- **Need input** — when a task enters arbitration or awaits plan approval

Browser notifications are also triggered when the Tasks tab detects status changes (requires notification permission).

## Task splitting

By default, the orchestrator prefers splitting tasks into small, well-defined subtasks. Each subtask is independently verifiable, and parallel delegation is preferred when possible.

You can override this behavior per-team using the `orchestrator_prompt` field:

```json
{
  "orchestrator_prompt": "Do NOT split this task. Execute it as a single delegation to one agent."
}
```

## What's next?

- [Creating Skills](docs:creating-skills) - teach ALF new abilities with auto-injection
- [Setting Up Tiers](docs:tier-setup) - customize how ALF picks the right model
