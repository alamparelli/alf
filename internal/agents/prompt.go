package agents

import (
	"fmt"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
)

// BuildOrchestratorPrompt generates the orchestrator's system prompt listing
// available teams/agents and the JSON delegation protocol.
// backend is the LLM backend used for the orchestrator brain (e.g. "codex", "cli").
func BuildOrchestratorPrompt(teams []*TeamConfig, taskDir, backend string) string {
	var sb strings.Builder

	sb.WriteString(strings.TrimSpace(memory.OrchestratorMD))
	sb.WriteString("\n\n")

	// Backend-specific instructions.
	if backend == "codex" {
		sb.WriteString(codexOrchestratorBlock)
		sb.WriteString("\n\n")
	}

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
		if tc.OrchestratorPrompt != "" {
			sb.WriteString(fmt.Sprintf("#### Instructions for team %s\n%s\n\n", tc.Name, tc.OrchestratorPrompt))
		}
		for _, a := range tc.Agents {
			sb.WriteString(fmt.Sprintf("- **%s/%s**: %s (tier: %s)\n", tc.Name, a.Name, a.Description, a.Tier))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

const codexOrchestratorBlock = `## Codex-Specific Rules (OVERRIDE GENERAL RULES)
- NEVER ASK QUESTIONS (Option D) unless the message is literally a single vague sentence with zero actionable details. If the message has more than 2 sentences of detail, it is detailed enough — act on it.
- NEVER ask "what task do you want?" or "which team should I use?" — YOU decide. The user already told you the task. Pick the best team yourself based on the task type.
- TEAM SELECTION: Choose the team whose description best matches the task. For code/app work → pick the dev team. For content → pick the content team. For SEO → pick SEO. Do NOT ask.
- OUTPUT A PLAN (Option A) WITHOUT questions on your FIRST iteration. Include your team choice in the plan. Then delegate immediately on the next iteration.
- Skip the gather step (Option E) if the task description already specifies what to change and where.`
