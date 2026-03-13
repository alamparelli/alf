package agents

import (
	"fmt"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
)

// BuildOrchestratorPrompt generates the orchestrator's system prompt listing
// available teams/agents and the JSON delegation protocol.
func BuildOrchestratorPrompt(teams []*TeamConfig, taskDir string) string {
	var sb strings.Builder

	sb.WriteString(strings.TrimSpace(memory.OrchestratorMD))
	sb.WriteString("\n\n")

	if taskDir != "" {
		sb.WriteString(fmt.Sprintf("## Task Directory\n%s\nAgents write deliverables in their working directory under this path.\n\n", taskDir))
	}

	if len(teams) == 0 {
		sb.WriteString("## Available Agents\nNo agent teams are configured.\n")
		return sb.String()
	}

	sb.WriteString("## Available Agents\n\n")
	for _, tc := range teams {
		sb.WriteString(fmt.Sprintf("### Team: %s\n%s\n\n", tc.Name, tc.Description))
		for _, a := range tc.Agents {
			sb.WriteString(fmt.Sprintf("- **%s/%s**: %s (tier: %s)\n", tc.Name, a.Name, a.Description, a.Tier))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
