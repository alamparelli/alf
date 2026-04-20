// Package agents is a thin re-export shim. The agent orchestrator now
// lives at internal/runtime/agents (moved during #340 R2b). Existing
// consumers keep importing internal/agents until R6 rewires them to
// runtime.Chat/Invoke directly.
package agents

import (
	rtagents "github.com/alamparelli/alf/internal/runtime/agents"
	provider "github.com/alamparelli/alf/internal/ai/provider"
)

// --- Types ---------------------------------------------------------------

type TeamConfig = rtagents.TeamConfig
type AgentConfig = rtagents.AgentConfig
type TierParams = rtagents.TierParams
type ResolveTierFunc = rtagents.ResolveTierFunc
type PlanStep = rtagents.PlanStep
type ApprovalDecision = rtagents.ApprovalDecision
type DelegateRequest = rtagents.DelegateRequest
type OrchestratorOutput = rtagents.OrchestratorOutput
type AgentResult = rtagents.AgentResult
type SkillLookup = rtagents.SkillLookup
type RunConfig = rtagents.RunConfig
type TaskMeta = rtagents.TaskMeta
type ResolveModelFunc = rtagents.ResolveModelFunc
type RunningTask = rtagents.RunningTask
type ResolveProviderFunc = rtagents.ResolveProviderFunc
type Orchestrator = rtagents.Orchestrator
type ProgressFunc = rtagents.ProgressFunc
type SessionManager = rtagents.SessionManager
type Store = rtagents.Store
type OrchestrationInputs = rtagents.OrchestrationInputs
type OrchestrationResult = rtagents.OrchestrationResult

// --- Constructors + functions -------------------------------------------

func NewOrchestrator(prov provider.Provider, store Store, dataDir string, resolveModel ResolveModelFunc, resolveTier ResolveTierFunc) *Orchestrator {
	return rtagents.NewOrchestrator(prov, store, dataDir, resolveModel, resolveTier)
}

func NewFileAgentStore(dir string) Store {
	return rtagents.NewFileAgentStore(dir)
}

func PrepareOrchestration(in OrchestrationInputs) OrchestrationResult {
	return rtagents.PrepareOrchestration(in)
}

func BuildOrchestratorPrompt(teams []*TeamConfig, taskDir, backend string) string {
	return rtagents.BuildOrchestratorPrompt(teams, taskDir, backend)
}
