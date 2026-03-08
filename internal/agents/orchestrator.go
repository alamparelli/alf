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
	defaultMaxIterations  = 10
	defaultOrchestratorTurns = 3 // low: orchestrator should output JSON quickly, not do deep tool work
	defaultGlobalTimeout  = 60 * time.Minute
	orchestratorKey       = "agent"
)

// ResolveModelFunc maps short model names to full CLI model names.
type ResolveModelFunc func(short string) string

// Orchestrator coordinates sub-agents via a resume loop.
// RunningTask tracks a live orchestrator task for cancellation.
type RunningTask struct {
	ID        string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Meta      *TaskMeta
}

type Orchestrator struct {
	provider     provider.Provider
	store        Store
	dataDir      string
	resolveModel ResolveModelFunc

	mu       sync.Mutex
	running  map[string]*RunningTask
}

// NewOrchestrator creates a new orchestrator.
func NewOrchestrator(prov provider.Provider, store Store, dataDir string, resolveModel ResolveModelFunc) *Orchestrator {
	return &Orchestrator{
		provider:     prov,
		store:        store,
		dataDir:      dataDir,
		resolveModel: resolveModel,
		running:      make(map[string]*RunningTask),
	}
}

// Running returns a snapshot of all currently running tasks.
func (o *Orchestrator) Running() []RunningTask {
	o.mu.Lock()
	defer o.mu.Unlock()
	tasks := make([]RunningTask, 0, len(o.running))
	for _, rt := range o.running {
		tasks = append(tasks, RunningTask{
			ID:        rt.ID,
			StartedAt: rt.StartedAt,
			Meta:      rt.Meta,
		})
	}
	return tasks
}

// Cancel stops a running task by ID. Returns true if the task was found and cancelled.
func (o *Orchestrator) Cancel(taskID string) bool {
	o.mu.Lock()
	rt, ok := o.running[taskID]
	o.mu.Unlock()
	if !ok {
		return false
	}
	log.Printf("[orchestrator] cancelling task %s", taskID)
	rt.Cancel()
	return true
}

// CancelAll stops all running tasks.
func (o *Orchestrator) CancelAll() int {
	o.mu.Lock()
	tasks := make([]*RunningTask, 0, len(o.running))
	for _, rt := range o.running {
		tasks = append(tasks, rt)
	}
	o.mu.Unlock()
	for _, rt := range tasks {
		log.Printf("[orchestrator] cancelling task %s", rt.ID)
		rt.Cancel()
	}
	return len(tasks)
}

// ProgressFunc reports status during orchestration.
type ProgressFunc func(phase, detail string)

// Run executes the orchestrator loop for a user message.
func (o *Orchestrator) Run(ctx context.Context, userMessage string, systemPrompts []string, rc RunConfig, onProgress ProgressFunc) (string, *TaskMeta, error) {
	teams := o.store.All()
	if len(teams) == 0 {
		return "", nil, fmt.Errorf("no agent teams configured")
	}

	taskID := fmt.Sprintf("%d", time.Now().UnixNano())
	taskDir := filepath.Join(o.dataDir, "agents", taskID)
	os.MkdirAll(taskDir, 0o755)

	log.Printf("[orchestrator] task %s started | teams=%d | message=%q", taskID, len(teams), truncate(userMessage, 120))

	meta := &TaskMeta{
		ID:        taskID,
		StartedAt: time.Now(),
		Status:    "running",
		Prompt:    userMessage,
	}

	// Persist team config and initial task state immediately.
	o.saveTeams(taskDir, teams)
	o.saveMeta(taskDir, meta)

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
	// Tier-level timeout overrides team-level timeout if set.
	if rc.TimeoutMin > 0 {
		globalTimeout = time.Duration(rc.TimeoutMin) * time.Minute
	}
	log.Printf("[orchestrator] global timeout=%s | task_dir=%s", globalTimeout, taskDir)
	ctx, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()

	// Register for cancellation tracking.
	o.mu.Lock()
	o.running[taskID] = &RunningTask{
		ID:        taskID,
		StartedAt: meta.StartedAt,
		Cancel:    cancel,
		Meta:      meta,
	}
	o.mu.Unlock()
	defer func() {
		// If status is still "running" when we exit (e.g. context cancelled,
		// panic recovery), update disk so the task isn't orphaned as invisible.
		if meta.Status == "running" {
			meta.Status = "interrupted"
			now := time.Now()
			meta.CompletedAt = &now
			o.saveMeta(taskDir, meta)
		}
		o.mu.Lock()
		delete(o.running, taskID)
		o.mu.Unlock()
	}()

	sm := newSessionManager()

	// Build orchestrator system prompt.
	orchPrompt := BuildOrchestratorPrompt(teams)
	allSystemPrompts := append(systemPrompts, orchPrompt)
	maxIterations := rc.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	orchModel := rc.Model
	if orchModel == "" {
		orchModel = "claude-opus-4-6"
		if o.resolveModel != nil {
			orchModel = o.resolveModel("opus")
		}
	}
	orchEffort := rc.Effort
	if orchEffort == "" {
		orchEffort = "high"
	}
	orchMaxTurns := rc.OrchestratorMaxTurns
	if orchMaxTurns <= 0 {
		orchMaxTurns = defaultOrchestratorTurns
	}
	log.Printf("[orchestrator] system prompts: %d total (%d user + orchestrator prompt) | max_iterations=%d model=%s effort=%s max_turns=%d", len(allSystemPrompts), len(systemPrompts), maxIterations, orchModel, orchEffort, orchMaxTurns)

	// First call: send user message.
	prompt := userMessage
	turnLimitRetries := 0
	const maxTurnLimitRetries = 2

	for iteration := 0; iteration < maxIterations; iteration++ {
		meta.Iterations = iteration + 1
		iterStart := time.Now()

		log.Printf("[orchestrator] ── iteration %d/%d ──", iteration+1, maxIterations)
		log.Printf("[orchestrator] prompt to orchestrator: %s", truncate(prompt, 200))

		if onProgress != nil {
			onProgress("thinking", fmt.Sprintf("iteration %d", iteration+1))
		}

		// Invoke orchestrator (read-only, uses taskDir as cwd).
		orchSessionID := sm.Get(orchestratorKey)

		hasResume := orchSessionID != ""
		log.Printf("[orchestrator] invoking model=%s effort=%s resume=%v", orchModel, orchEffort, hasResume)

		params := provider.Params{
			Model:         orchModel,
			SystemPrompts: allSystemPrompts,
			ResumeID:      orchSessionID,
			DataDir:       taskDir,
			Effort:        orchEffort,
			MaxTurns:      orchMaxTurns,
			// No tools for orchestrator brain — it must only produce JSON delegation output.
			// Tools are for sub-agents, not the coordinator.
		}

		result, err := o.provider.Invoke(ctx, prompt, params, nil)

		// Retry without resume if session expired.
		if err != nil && orchSessionID != "" && strings.Contains(err.Error(), "No conversation found") {
			log.Printf("[orchestrator] session expired, retrying without resume")
			sm.Clear(orchestratorKey)
			params.ResumeID = ""
			result, err = o.provider.Invoke(ctx, prompt, params, nil)
		}

		if err != nil {
			log.Printf("[orchestrator] FAILED iteration %d: %v", iteration+1, err)
			meta.Status = "failed"
			o.saveMeta(taskDir, meta)
			return "", meta, fmt.Errorf("orchestrator invoke: %w", err)
		}

		iterDur := time.Since(iterStart)
		meta.TotalCost += result.CostUSD
		if result.SessionID != "" {
			sm.Set(orchestratorKey, result.SessionID)
		}

		log.Printf("[orchestrator] response received: %dms $%.4f %d chars session=%s",
			iterDur.Milliseconds(), result.CostUSD, len(result.Text), truncate(result.SessionID, 12))
		log.Printf("[orchestrator] raw output: %s", truncate(result.Text, 300))

		// Detect orchestrator turn limit — retry a limited number of times.
		if strings.Contains(result.Text, "Turn limit reached") {
			turnLimitRetries++
			if turnLimitRetries > maxTurnLimitRetries {
				log.Printf("[orchestrator] ✗ orchestrator hit turn limit %d times — aborting", turnLimitRetries)
				meta.Status = "failed"
				o.saveMeta(taskDir, meta)
				return "", meta, fmt.Errorf("orchestrator repeatedly hit turn limit (%d retries) — try increasing max_turns in the orchestrator tier config", maxTurnLimitRetries)
			}
			log.Printf("[orchestrator] ⚠ orchestrator hit turn limit (%d/%d retries) — clearing session", turnLimitRetries, maxTurnLimitRetries)
			sm.Clear(orchestratorKey)
			prompt = userMessage
			continue
		}

		// Parse orchestrator output.
		output := parseOrchestratorOutput(result.Text)

		// Final response — done.
		if output.Response != "" {
			log.Printf("[orchestrator] ✓ final response received (%d chars)", len(output.Response))
			meta.Status = "completed"
			meta.Response = output.Response
			now := time.Now()
			meta.CompletedAt = &now
			o.saveMeta(taskDir, meta)
			totalDur := time.Since(meta.StartedAt)
			log.Printf("[orchestrator] task %s completed: %d iterations %dms $%.4f",
				taskID, meta.Iterations, totalDur.Milliseconds(), meta.TotalCost)
			return output.Response, meta, nil
		}

		// No delegates and no response — treat as empty iteration.
		if len(output.Delegates) == 0 {
			log.Printf("[orchestrator] ⚠ no delegates and no response — nudging")
			prompt = `{"agent_results": [], "note": "No delegates provided. Either delegate to agents or provide a final response."}`
			continue
		}

		// Log delegate plan.
		agentNames := make([]string, len(output.Delegates))
		for i, d := range output.Delegates {
			_, name := splitTeamAgent(d.Agent)
			agentNames[i] = name
			log.Printf("[orchestrator]   [%d] agent=%s task=%s", i+1, d.Agent, truncate(d.Task, 100))
		}
		log.Printf("[orchestrator] delegating to %d agent(s): %s", len(output.Delegates), strings.Join(agentNames, ", "))

		if onProgress != nil {
			onProgress("planning", fmt.Sprintf("Dispatching %d agents: %s", len(output.Delegates), strings.Join(agentNames, ", ")))
		}

		// Execute delegates.
		agentResults := o.executeDelegates(ctx, output.Delegates, teams, sm, taskDir, meta, onProgress)

		// Log results summary.
		log.Printf("[orchestrator] delegates completed:")
		for _, ar := range agentResults {
			if ar.Error != "" {
				log.Printf("[orchestrator]   ✗ %s: error=%s (%dms)", ar.Agent, truncate(ar.Error, 100), ar.Duration.Milliseconds())
			} else {
				log.Printf("[orchestrator]   ✓ %s: %d chars $%.4f (%dms)", ar.Agent, len(ar.Text), ar.CostUSD, ar.Duration.Milliseconds())
			}
		}

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
		log.Printf("[orchestrator] resume payload: %d bytes, running cost=$%.4f", len(data), meta.TotalCost)

		if onProgress != nil {
			onProgress("synthesizing", "")
		}
	}

	// Max iterations exceeded.
	log.Printf("[orchestrator] ✗ task %s hit max iterations (%d)", taskID, maxIterations)
	meta.Status = "timeout"
	now := time.Now()
	meta.CompletedAt = &now
	o.saveMeta(taskDir, meta)
	return "", meta, fmt.Errorf("max iterations (%d) exceeded", maxIterations)
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

	// Track how many times each agent appears to create unique session keys.
	agentCount := make(map[string]int, len(delegates))
	type indexedDelegate struct {
		DelegateRequest
		index int
	}
	indexed := make([]indexedDelegate, len(delegates))
	for i, d := range delegates {
		agentCount[d.Agent]++
		indexed[i] = indexedDelegate{d, agentCount[d.Agent]}
	}

	for _, id := range indexed {
		wg.Add(1)
		go func(d DelegateRequest, idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Use unique session key when same agent is delegated multiple times.
			sessionKey := d.Agent
			if agentCount[d.Agent] > 1 {
				sessionKey = fmt.Sprintf("%s#%d", d.Agent, idx)
			}
			ar := o.invokeAgentWithKey(ctx, d, sessionKey, sm, taskDir, onProgress)

			mu.Lock()
			results = append(results, ar)
			meta.AgentCalls = append(meta.AgentCalls, ar)
			meta.TotalCost += ar.CostUSD
			mu.Unlock()
		}(id.DelegateRequest, id.index)
	}

	wg.Wait()
	return results
}

// invokeAgentWithKey calls a single sub-agent using the given session key.
func (o *Orchestrator) invokeAgentWithKey(
	ctx context.Context,
	d DelegateRequest,
	sessionKey string,
	sm *SessionManager,
	taskDir string,
	onProgress ProgressFunc,
) AgentResult {
	start := time.Now()
	teamName, agentName := splitTeamAgent(d.Agent)

	_, ac, ok := o.store.GetAgent(d.Agent)
	if !ok {
		log.Printf("[orchestrator] agent %q not found in store", d.Agent)
		return AgentResult{
			Agent:    d.Agent,
			Error:    fmt.Sprintf("agent %q not found", d.Agent),
			Duration: time.Since(start),
		}
	}

	if onProgress != nil {
		onProgress("agent", fmt.Sprintf("%s/%s", teamName, agentName))
	}

	// Write-capable agents get their own working directory; read-only agents share taskDir.
	agentDir := taskDir
	if ac.WriteCapable {
		agentDir = filepath.Join(taskDir, fmt.Sprintf("%s-%s", teamName, agentName))
		if sessionKey != d.Agent {
			agentDir = filepath.Join(taskDir, fmt.Sprintf("%s-%s-%s", teamName, agentName, sessionKey[strings.LastIndex(sessionKey, "#")+1:]))
		}
		os.MkdirAll(agentDir, 0o755)
	}

	model := ac.Model
	if o.resolveModel != nil {
		model = o.resolveModel(ac.Model)
	}

	sessionID := sm.Get(sessionKey)
	hasResume := sessionID != ""
	log.Printf("[orchestrator] → agent %s/%s: model=%s effort=%s write=%v max_turns=%d resume=%v",
		teamName, agentName, model, ac.Effort, ac.WriteCapable, ac.MaxTurns, hasResume)
	log.Printf("[orchestrator]   task: %s", truncate(d.Task, 150))

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
		log.Printf("[orchestrator]   agent %s/%s session expired, retrying", teamName, agentName)
		sm.Clear(sessionKey)
		params.ResumeID = ""
		result, err = o.provider.Invoke(ctx, d.Task, params, nil)
	}

	dur := time.Since(start)
	if err != nil {
		log.Printf("[orchestrator] ✗ agent %s/%s failed after %dms: %v", teamName, agentName, dur.Milliseconds(), err)
		if onProgress != nil {
			onProgress("agent_done", fmt.Sprintf("%s ✗ (%ds)", agentName, int(dur.Seconds())))
		}
		return AgentResult{
			Agent:    d.Agent,
			Error:    err.Error(),
			Duration: dur,
		}
	}

	if result.SessionID != "" {
		sm.Set(sessionKey, result.SessionID)
	}

	log.Printf("[orchestrator] ← agent %s/%s: %dms $%.4f %d chars session=%s",
		teamName, agentName, dur.Milliseconds(), result.CostUSD, len(result.Text), truncate(result.SessionID, 12))

	if onProgress != nil {
		onProgress("agent_done", fmt.Sprintf("%s ✓ (%ds, $%.4f)", agentName, int(dur.Seconds()), result.CostUSD))
	}

	return AgentResult{
		Agent:    d.Agent,
		Text:     result.Text,
		CostUSD:  result.CostUSD,
		Duration: dur,
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
		// Not valid JSON — do NOT treat as response; force re-delegation.
		log.Printf("[orchestrator] ⚠ output is not valid JSON, will nudge for proper delegation")
		return OrchestratorOutput{} // empty = triggers nudge loop
	}

	return out
}

// truncate shortens a string to maxLen, appending "…" if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// saveTeams writes teams.json to the task directory with the agent config snapshot.
func (o *Orchestrator) saveTeams(taskDir string, teams []*TeamConfig) {
	data, err := json.MarshalIndent(teams, "", "  ")
	if err != nil {
		log.Printf("[orchestrator] failed to marshal teams: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(taskDir, "teams.json"), data, 0o644); err != nil {
		log.Printf("[orchestrator] failed to write teams.json: %v", err)
	}
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
