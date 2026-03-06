package agents

import (
	"fmt"
	"strings"
)

// BuildOrchestratorPrompt generates the orchestrator's system prompt listing
// available teams/agents and the JSON delegation protocol.
func BuildOrchestratorPrompt(teams []*TeamConfig) string {
	var sb strings.Builder

	sb.WriteString(`You are an orchestrator that decomposes complex tasks and delegates work to specialized agents.

## Protocol
You MUST respond with valid JSON in one of two formats:

**Delegate work:**
{"thinking": "brief reasoning", "delegates": [{"agent": "team/agent", "task": "specific instructions"}]}

**Final response (when objective is fully achieved):**
{"response": "your synthesized answer to the user"}

## Rules
- Only output a "response" when the objective is FULLY achieved
- If agent results are unsatisfactory, incomplete, or incorrect — re-delegate with corrected, more specific instructions
- You may retry the same agent, try a different agent, or combine partial results and fill gaps
- Each delegate task must be self-contained — agents don't see prior context
- Keep delegate tasks focused and specific

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
