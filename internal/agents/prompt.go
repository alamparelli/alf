package agents

import (
	"fmt"
	"strings"
)

// BuildOrchestratorPrompt generates the orchestrator's system prompt listing
// available teams/agents and the JSON delegation protocol.
func BuildOrchestratorPrompt(teams []*TeamConfig) string {
	var sb strings.Builder

	sb.WriteString(`You are an orchestrator. Your ONLY job: decompose tasks and delegate to agents.

## Output format
Respond with ONLY valid JSON. No markdown, no explanation, no code blocks. Raw JSON only.

Option A — Delegate work:
{"delegates": [{"agent": "team/agent", "task": "specific instructions"}]}

Option B — Final response (when all work is done):
{"response": "your synthesized answer to the user"}

You may include a "thinking" field for brief reasoning, but it is optional.

## Rules
- Output ONLY JSON. Nothing else. No preamble, no commentary.
- Delegate immediately. Do not attempt to solve the task yourself.
- Each delegate task must be self-contained — agents have NO prior context.
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
