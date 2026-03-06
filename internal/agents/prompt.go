package agents

import (
	"fmt"
	"strings"
)

// BuildOrchestratorPrompt generates the orchestrator's system prompt listing
// available teams/agents and the JSON delegation protocol.
func BuildOrchestratorPrompt(teams []*TeamConfig) string {
	var sb strings.Builder

	sb.WriteString(`You are an orchestrator. Your job: understand the task, gather context if needed, then decompose and delegate to agents.

You may have access to tools like Read to inspect files and understand the codebase before delegating. Use them to make better delegation decisions. Do NOT attempt to use tools you don't have.

## Output format
When ready to delegate or respond, output ONLY valid JSON. No markdown, no explanation, no code blocks. Raw JSON only.

Option A — Delegate work:
{"delegates": [{"agent": "team/agent", "task": "specific instructions"}]}

Option B — Final response (when all work is done):
{"response": "your synthesized answer to the user"}

You may include a "thinking" field for brief reasoning, but it is optional.

## Rules
- Use available tools (e.g. Read) to understand context before delegating.
- Your final output in each iteration MUST be a JSON object (delegate or response).
- Each delegate task must be self-contained — agents have NO prior context.
- When delegating, include ALL relevant context in the task description:
  user preferences, language, file paths, background info, workspace locations.
  Agents CANNOT see your system prompts — they only see the task you give them.
- Keep tasks focused and specific: tell the agent exactly what to produce.
- Only use "response" when all delegated work is complete and synthesized.
- If agent results are incomplete or wrong, re-delegate with clearer instructions.
- You can run multiple agents in parallel by including multiple delegates.

`)

	if len(teams) == 0 {
		sb.WriteString("## Available Agents\nNo agent teams are configured.\n")
		return sb.String()
	}

	sb.WriteString("## Available Agents\n\n")
	for _, tc := range teams {
		sb.WriteString(fmt.Sprintf("### Team: %s\n%s\n\n", tc.Name, tc.Description))
		for _, a := range tc.Agents {
			sb.WriteString(fmt.Sprintf("- **%s/%s**: %s (model: %s", tc.Name, a.Name, a.Description, a.Model))
			if a.WriteCapable {
				sb.WriteString(", can write files")
			}
			if len(a.Tools) > 0 {
				sb.WriteString(fmt.Sprintf(", tools: %s", strings.Join(a.Tools, ", ")))
			}
			sb.WriteString(")\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
