package agents

import (
	"log"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/skills"
)

// OrchestrationInputs gathers the common inputs needed to prepare an orchestration run.
// Every channel (Telegram, Control Center, Scheduler, etc.) fills this struct
// and calls PrepareOrchestration() to get standardized system prompts and RunConfig.
type OrchestrationInputs struct {
	// Required
	UserMessage string     // raw user message (or enriched with reply context)
	DataDir     string     // workspace data directory (e.g. /home/alf/data)
	ContextDir  string     // context directory for memory injection
	Source      string     // "router", "telegram", "chat", "schedule"

	// Tier-level overrides (from resolved tier config)
	Model                string // full model name for orchestrator brain
	Backend              string // backend ("", "cli", "openrouter")
	Effort               string // effort level
	MaxTurns             int    // max turns per sub-agent
	OrchestratorMaxTurns int    // turns per orchestrator brain call
	MaxIterations        int    // max orchestrate→delegate cycles
	TimeoutMin           int    // global timeout in minutes

	// Optional enrichments
	RecallBlock         string       // pre-built memory recall block (caller runs recallMemories)
	SkillStore          skills.Store // skill store for catalog + trigger matching
	ConversationContext string       // recent conversation summary (from BuildRouterContext or similar)
	Team                string       // team name if launched for a specific team
	NeedValidation      bool         // block after plan for user approval
}

// OrchestrationResult holds the prepared system prompts and RunConfig
// ready to pass to Orchestrator.Run().
type OrchestrationResult struct {
	SystemPrompts []string
	Config        RunConfig
}

// PrepareOrchestration standardizes orchestrator preparation across all channels.
// It builds the system prompts (recall, skill catalog, workspace summary, conversation context)
// and a fully populated RunConfig. The caller only needs to provide OrchestrationInputs
// and then call Orchestrator.Run() with the result.
func PrepareOrchestration(in OrchestrationInputs) OrchestrationResult {
	var sysPrompts []string

	// 1. Memory recall block (semantic search results).
	if in.RecallBlock != "" {
		sysPrompts = append(sysPrompts, in.RecallBlock)
	}

	// 2. Skill catalog + trigger matching.
	var skillInjections []string
	if in.SkillStore != nil {
		if catalog := skills.BuildCatalog(in.SkillStore); catalog != "" {
			sysPrompts = append(sysPrompts, catalog)
		}
		if matched := skills.MatchTriggers(in.SkillStore, in.UserMessage); len(matched) > 0 {
			names := make([]string, len(matched))
			for i, sk := range matched {
				names[i] = sk.Name
				if sk.Prompt != "" {
					skillInjections = append(skillInjections, sk.Prompt)
				}
			}
			log.Printf("[orchestration] matched skills %v (%d prompts)", names, len(skillInjections))
		}
	}

	// 3. Workspace summary (ls of data dir).
	if in.DataDir != "" {
		if ws := memory.WorkspaceSummary(in.DataDir); ws != "" {
			sysPrompts = append(sysPrompts, ws)
		}
	}

	// 4. Conversation context (recent exchanges for orchestrator awareness).
	if in.ConversationContext != "" {
		var sb strings.Builder
		sb.WriteString("=== [Recent conversation] ===\n")
		sb.WriteString(in.ConversationContext)
		sysPrompts = append(sysPrompts, sb.String())
	}

	// Build RunConfig.
	rc := RunConfig{
		Model:                in.Model,
		Backend:              in.Backend,
		Effort:               in.Effort,
		MaxTurns:             in.MaxTurns,
		OrchestratorMaxTurns: in.OrchestratorMaxTurns,
		MaxIterations:        in.MaxIterations,
		TimeoutMin:           in.TimeoutMin,
		Source:               in.Source,
		SkillPrompts:         skillInjections,
		MemoryContext:        memory.CollectAgentContext(in.ContextDir),
		NeedValidation:       in.NeedValidation,
		Team:                 in.Team,
		OriginalRequest:      in.UserMessage,
		ConversationContext:  in.ConversationContext,
	}

	return OrchestrationResult{
		SystemPrompts: sysPrompts,
		Config:        rc,
	}
}
