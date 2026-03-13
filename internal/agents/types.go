package agents

import "time"

// TeamConfig defines a group of specialized agents.
type TeamConfig struct {
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	MaxAgentsPerReq  int           `json:"max_agents_per_request"`
	GlobalTimeoutMin int           `json:"global_timeout_minutes"`
	Agents           []AgentConfig `json:"agents"`
}

// AgentConfig defines a single sub-agent within a team.
type AgentConfig struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Tier         string `json:"tier"`
	SystemPrompt string `json:"system_prompt"`
}

// TierParams holds resolved execution parameters from a tier.
type TierParams struct {
	Model        string
	Backend      string
	Tools        []string
	Effort       string
	WriteCapable bool
	MaxTurns     int
	SystemPrompt string // tier-level system prompt (combined with agent's)
}

// ResolveTierFunc maps a tier name to its execution parameters.
type ResolveTierFunc func(tierName string) (TierParams, bool)

// DelegateRequest is a single delegation instruction from the orchestrator.
type DelegateRequest struct {
	Agent string `json:"agent"` // "team/agent" format
	Task  string `json:"task"`
}

// OrchestratorOutput is the JSON protocol the orchestrator produces.
type OrchestratorOutput struct {
	Delegates []DelegateRequest `json:"delegates,omitempty"`
	Response  string            `json:"response,omitempty"`
	Thinking  string            `json:"thinking,omitempty"`
}

// AgentResult holds the outcome of a single sub-agent invocation.
type AgentResult struct {
	Agent    string        `json:"agent"`
	Task     string        `json:"task,omitempty"`
	Status   string        `json:"status"` // "working", "completed", "failed"
	Model    string        `json:"model,omitempty"`
	Text     string        `json:"text,omitempty"`
	Error    string        `json:"error,omitempty"`
	CostUSD  float64       `json:"cost_usd"`
	Duration time.Duration `json:"-"`
}

// agentResultJSON is the JSON-friendly version sent back to the orchestrator.
type agentResultJSON struct {
	Agent      string  `json:"agent"`
	Result     string  `json:"result,omitempty"`
	Error      string  `json:"error,omitempty"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMs int64   `json:"duration_ms"`
}

// RunConfig holds tier-level settings for an orchestrator run.
type RunConfig struct {
	Model                string   // full model name for the orchestrator brain
	Backend              string   // backend for the orchestrator brain ("" or "cli" = default CLI)
	Effort               string   // effort level (e.g. "high")
	MaxIterations        int      // max orchestrate→delegate cycles (0 = default 10)
	MaxTurns             int      // max turns per sub-agent call (0 = use agent config)
	OrchestratorMaxTurns int      // max turns per orchestrator brain call (0 = default 3)
	TimeoutMin           int      // global timeout in minutes (0 = default 60)
	SkillPrompts         []string // skill prompts injected into every sub-agent
	MemoryContext        []string // memory/context prompts injected into every sub-agent
}

// TaskMeta tracks the lifecycle of an orchestration run.
type TaskMeta struct {
	ID          string        `json:"id"`
	Prompt      string        `json:"prompt,omitempty"`
	Response    string        `json:"response,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
	Iterations  int           `json:"iterations"`
	TotalCost   float64       `json:"total_cost_usd"`
	AgentCalls  []AgentResult `json:"agent_calls"`
	Status      string        `json:"status"` // running, completed, failed, timeout
}
