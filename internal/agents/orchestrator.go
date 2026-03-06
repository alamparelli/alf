package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/provider"
)

const (
	defaultMaxIterations = 10
	defaultGlobalTimeout = 60 * time.Minute
	orchestratorKey      = "orchestrator"
)

// ResolveModelFunc maps short model names to full CLI model names.
type ResolveModelFunc func(short string) string

// Orchestrator coordinates sub-agents via a resume loop.
type Orchestrator struct {
	provider     provider.Provider
	store        Store
	dataDir      string
	resolveModel ResolveModelFunc
}

// NewOrchestrator creates a new orchestrator.
func NewOrchestrator(prov provider.Provider, store Store, dataDir string, resolveModel ResolveModelFunc) *Orchestrator {
	return &Orchestrator{
		provider:     prov,
		store:        store,
		dataDir:      dataDir,
		resolveModel: resolveModel,
	}
}

// ProgressFunc reports status during orchestration.
type ProgressFunc func(phase, detail string)

// Run executes the orchestrator loop for a user message.
func (o *Orchestrator) Run(ctx context.Context, userMessage string, systemPrompts []string, onProgress ProgressFunc) (string, *TaskMeta, error) {
	teams := o.store.All()
	if len(teams) == 0 {
		return "", nil, fmt.Errorf("no agent teams configured")
	}

	taskID := fmt.Sprintf("%d", time.Now().UnixNano())
	taskDir := filepath.Join(o.dataDir, "agents", taskID)
	os.MkdirAll(taskDir, 0o755)

	meta := &TaskMeta{
		ID:        taskID,
		StartedAt: time.Now(),
		Status:    "running",
	}

	// Determine global timeout from max team timeout.
	globalTimeout := defaultGlobalTimeout
	for _, tc := range teams {
		if tc.GlobalTimeoutMin > 0 {
			d := time.Duration(tc.GlobalTimeoutMin) * time.Minute
			if d > globalTimeout {
				globalTimeout = d
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()

	sm := newSessionManager()

	// Build orchestrator system prompt.
	orchPrompt := BuildOrchestratorPrompt(teams)
	allSystemPrompts := append(systemPrompts, orchPrompt)

	// First call: send user message.
	prompt := userMessage

	for iteration := 0; iteration < defaultMaxIterations; iteration++ {
		meta.Iterations = iteration + 1

		if onProgress != nil {
			onProgress("thinking", fmt.Sprintf("iteration %d", iteration+1))
		}

		// Invoke orchestrator.
		orchSessionID := sm.Get(orchestratorKey)
		orchDir := filepath.Join(taskDir, "orchestrator")
		os.MkdirAll(orchDir, 0o755)

		model := "claude-opus-4-6"
		if o.resolveModel != nil {
			model = o.resolveModel("opus")
		}

		params := provider.Params{
			Model:         model,
			SystemPrompts: allSystemPrompts,
			ResumeID:      orchSessionID,
			DataDir:       orchDir,
			Effort:        "high",
		}

		result, err := o.provider.Invoke(ctx, prompt, params, nil)

		// Retry without resume if session expired.
		if err != nil && orchSessionID != "" && strings.Contains(err.Error(), "No conversation found") {
			log.Printf("[orchestrator] session expired, starting fresh")
			sm.Clear(orchestratorKey)
			params.ResumeID = ""
			result, err = o.provider.Invoke(ctx, prompt, params, nil)
		}

		if err != nil {
			meta.Status = "failed"
			o.saveMeta(taskDir, meta)
			return "", meta, fmt.Errorf("orchestrator invoke: %w", err)
		}

		meta.TotalCost += result.CostUSD
		if result.SessionID != "" {
			sm.Set(orchestratorKey, result.SessionID)
		}

		// Parse orchestrator output.
		output := parseOrchestratorOutput(result.Text)

		// Final response — done.
		if output.Response != "" {
			meta.Status = "completed"
			now := time.Now()
			meta.CompletedAt = &now
			o.saveMeta(taskDir, meta)
			return output.Response, meta, nil
		}

		// No delegates and no response — treat as empty iteration.
		if len(output.Delegates) == 0 {
			// Ask orchestrator again with nudge.
			prompt = `{"agent_results": [], "note": "No delegates provided. Either delegate to agents or provide a final response."}`
			continue
		}

		// Execute delegates.
		agentResults := o.executeDelegates(ctx, output.Delegates, teams, sm, taskDir, meta, onProgress)

		// Build results JSON for resume.
		var resultsJSON []agentResultJSON
		for _, ar := range agentResults {
			resultsJSON = append(resultsJSON, agentResultJSON{
				Agent:      ar.Agent,
				Result:     ar.Text,
				Error:      ar.Error,
				CostUSD:    ar.CostUSD,
				DurationMs: ar.Duration.Milliseconds(),
			})
		}

		resumeData := struct {
			AgentResults []agentResultJSON `json:"agent_results"`
			Iteration    int               `json:"iteration"`
			TotalCostUSD float64           `json:"total_cost_usd"`
		}{
			AgentResults: resultsJSON,
			Iteration:    iteration + 2,
			TotalCostUSD: meta.TotalCost,
		}

		data, _ := json.Marshal(resumeData)
		prompt = string(data)
	}

	// Max iterations exceeded.
	meta.Status = "timeout"
	now := time.Now()
	meta.CompletedAt = &now
	o.saveMeta(taskDir, meta)
	return "", meta, fmt.Errorf("max iterations (%d) exceeded", defaultMaxIterations)
}

// executeDelegates runs sub-agents concurrently and collects results.
func (o *Orchestrator) executeDelegates(
	ctx context.Context,
	delegates []DelegateRequest,
	teams []*TeamConfig,
	sm *SessionManager,
	taskDir string,
	meta *TaskMeta,
	onProgress ProgressFunc,
) []AgentResult {
	// Find max concurrent limit from the relevant teams.
	maxConcurrent := 0
	for _, d := range delegates {
		teamName, _ := splitTeamAgent(d.Agent)
		for _, tc := range teams {
			if tc.Name == teamName && tc.MaxAgentsPerReq > maxConcurrent {
				maxConcurrent = tc.MaxAgentsPerReq
			}
		}
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}

	// Truncate delegates if exceeding limit.
	if len(delegates) > maxConcurrent {
		log.Printf("[orchestrator] truncating %d delegates to %d (team limit)", len(delegates), maxConcurrent)
		delegates = delegates[:maxConcurrent]
	}

	var (
		mu      sync.Mutex
		results []AgentResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrent)
	)

	for _, d := range delegates {
		wg.Add(1)
		go func(d DelegateRequest) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ar := o.invokeAgent(ctx, d, sm, taskDir, onProgress)

			mu.Lock()
			results = append(results, ar)
			meta.AgentCalls = append(meta.AgentCalls, ar)
			meta.TotalCost += ar.CostUSD
			mu.Unlock()
		}(d)
	}

	wg.Wait()
	return results
}

// invokeAgent calls a single sub-agent.
func (o *Orchestrator) invokeAgent(
	ctx context.Context,
	d DelegateRequest,
	sm *SessionManager,
	taskDir string,
	onProgress ProgressFunc,
) AgentResult {
	start := time.Now()
	teamName, agentName := splitTeamAgent(d.Agent)

	_, ac, ok := o.store.GetAgent(d.Agent)
	if !ok {
		return AgentResult{
			Agent:    d.Agent,
			Error:    fmt.Sprintf("agent %q not found", d.Agent),
			Duration: time.Since(start),
		}
	}

	if onProgress != nil {
		onProgress("agent", fmt.Sprintf("%s/%s", teamName, agentName))
	}

	agentDir := filepath.Join(taskDir, fmt.Sprintf("%s-%s", teamName, agentName))
	os.MkdirAll(agentDir, 0o755)

	model := ac.Model
	if o.resolveModel != nil {
		model = o.resolveModel(ac.Model)
	}

	sessionID := sm.Get(d.Agent)
	params := provider.Params{
		Model:         model,
		Tools:         ac.Tools,
		WriteCapable:  ac.WriteCapable,
		Effort:        ac.Effort,
		MaxTurns:      ac.MaxTurns,
		SystemPrompts: []string{ac.SystemPrompt},
		ResumeID:      sessionID,
		DataDir:       agentDir,
	}

	result, err := o.provider.Invoke(ctx, d.Task, params, nil)

	// Retry without resume if session expired.
	if err != nil && sessionID != "" && strings.Contains(err.Error(), "No conversation found") {
		sm.Clear(d.Agent)
		params.ResumeID = ""
		result, err = o.provider.Invoke(ctx, d.Task, params, nil)
	}

	if err != nil {
		return AgentResult{
			Agent:    d.Agent,
			Error:    err.Error(),
			Duration: time.Since(start),
		}
	}

	if result.SessionID != "" {
		sm.Set(d.Agent, result.SessionID)
	}

	return AgentResult{
		Agent:    d.Agent,
		Text:     result.Text,
		CostUSD:  result.CostUSD,
		Duration: time.Since(start),
	}
}

// parseOrchestratorOutput parses the orchestrator's JSON output.
// Falls back to treating the entire text as a plain text response.
func parseOrchestratorOutput(text string) OrchestratorOutput {
	text = strings.TrimSpace(text)

	// Try to find JSON in the text (may be wrapped in markdown code blocks).
	jsonStr := text
	if idx := strings.Index(text, "{"); idx >= 0 {
		// Find the matching closing brace.
		depth := 0
		for i := idx; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					jsonStr = text[idx : i+1]
					goto parse
				}
			}
		}
	}

parse:
	var out OrchestratorOutput
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		// Not valid JSON — treat entire text as final response.
		return OrchestratorOutput{Response: text}
	}

	// If neither response nor delegates with content, treat as plain text.
	if out.Response == "" && out.Delegates == nil {
		return OrchestratorOutput{Response: text}
	}

	return out
}

// saveMeta writes task.json to the task directory.
func (o *Orchestrator) saveMeta(taskDir string, meta *TaskMeta) {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		log.Printf("[orchestrator] failed to marshal task meta: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), data, 0o644); err != nil {
		log.Printf("[orchestrator] failed to write task meta: %v", err)
	}
}
