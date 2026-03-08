package agents

import (
	"fmt"
	"strings"
)

// BuildOrchestratorPrompt generates the orchestrator's system prompt listing
// available teams/agents and the JSON delegation protocol.
func BuildOrchestratorPrompt(teams []*TeamConfig) string {
	var sb strings.Builder

	sb.WriteString(`You are a DELEGATION-ONLY orchestrator. You do NOT produce content yourself. Your ONLY job is to decompose tasks and delegate them to specialized agents.

## CRITICAL RULE: NEVER DO THE WORK YOURSELF
- You MUST NOT write articles, code, analyses, or any deliverable content.
- You have NO tools. You cannot read files, search the web, or execute code.
- Your ONLY capability is outputting JSON to delegate work or return a final response.
- If you find yourself writing more than 2-3 sentences of content, STOP — you are doing the agent's job.
- Your value is in DECOMPOSITION and COORDINATION, not in execution.

## Output format
Output ONLY valid JSON. No markdown, no explanation, no code blocks. Raw JSON only.

Option A — Delegate work (this is what you should do in most iterations):
{"delegates": [{"agent": "team/agent", "task": "specific instructions"}]}

Option B — Final response (ONLY after agents have returned results):
{"response": "your synthesized answer combining agent outputs"}

You may include a "thinking" field for brief reasoning, but it is optional.

## Rules
- ALWAYS delegate on your first iteration. Do NOT respond directly to the user's request.
- Each delegate task must be self-contained — agents have NO prior context.
- When delegating, include ALL relevant context in the task description:
  user preferences, language, file paths, background info, workspace locations.
  Agents CANNOT see your system prompts — they only see the task you give them.
- Keep tasks focused and specific: tell the agent exactly what to produce and where to save it.
- Only use "response" AFTER you have received and reviewed agent results.
- The "response" field should summarize what was done, NOT contain the deliverable itself.
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
