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

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/tooling"
)

const (
	defaultMaxIterations     = 20
	defaultOrchestratorTurns = 3 // low: orchestrator should output JSON quickly, not do deep tool work
	defaultGlobalTimeout     = 60 * time.Minute
	orchestratorKey          = "agent"
)

// ResolveModelFunc maps short model names to full CLI model names.
type ResolveModelFunc func(short string) string

// Orchestrator coordinates sub-agents via a resume loop.
// RunningTask tracks a live orchestrator task for cancellation.
type RunningTask struct {
	ID         string
	StartedAt  time.Time
	Cancel     context.CancelFunc
	Meta       *TaskMeta
	ApprovalCh chan ApprovalDecision
}

// ResolveProviderFunc maps a backend name to a provider.
// Empty string or "cli" should return the default CLI provider.
type ResolveProviderFunc func(backend string) provider.Provider

type Orchestrator struct {
	provider        provider.Provider
	store           Store
	dataDir         string
	resolveModel    ResolveModelFunc
	resolveTier     ResolveTierFunc
	resolveProvider ResolveProviderFunc // optional: resolve provider by backend name
	toolRegistry    *tooling.Registry   // optional: tool schemas for API agentic loop
	toolExecutor    *tooling.Executor   // optional: tool subprocess runner

	mu       sync.Mutex
	running  map[string]*RunningTask
}

// NewOrchestrator creates a new orchestrator.
func NewOrchestrator(prov provider.Provider, store Store, dataDir string, resolveModel ResolveModelFunc, resolveTier ResolveTierFunc) *Orchestrator {
	return &Orchestrator{
		provider:     prov,
		store:        store,
		dataDir:      dataDir,
		resolveModel: resolveModel,
		resolveTier:  resolveTier,
		running:      make(map[string]*RunningTask),
	}
}

// SetResolveProvider sets the function used to resolve providers by backend name.
func (o *Orchestrator) SetResolveProvider(fn ResolveProviderFunc) {
	o.resolveProvider = fn
}

// SetTooling configures the tool registry and executor for API-tier agentic tool loops.
func (o *Orchestrator) SetTooling(registry *tooling.Registry, executor *tooling.Executor) {
	o.toolRegistry = registry
	o.toolExecutor = executor
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

// DeleteTask removes a completed task's directory from disk.
// Returns true if the task was found and deleted. Cannot delete running tasks.
func (o *Orchestrator) DeleteTask(taskID string) bool {
	o.mu.Lock()
	_, running := o.running[taskID]
	o.mu.Unlock()
	if running {
		return false
	}
	taskDir := filepath.Join(o.dataDir, "agents", taskID)
	if _, err := os.Stat(taskDir); os.IsNotExist(err) {
		return false
	}
	os.RemoveAll(taskDir)
	log.Printf("[orchestrator] deleted task %s", taskID)
	return true
}

// Approve sends an approval decision to a task awaiting validation.
// Returns true if the decision was delivered.
func (o *Orchestrator) Approve(taskID string, decision ApprovalDecision) bool {
	o.mu.Lock()
	rt, ok := o.running[taskID]
	o.mu.Unlock()
	if !ok || rt.Meta.Status != "awaiting_approval" {
		return false
	}
	select {
	case rt.ApprovalCh <- decision:
		return true
	default:
		return false
	}
}

// providerFor returns the provider for the given backend, falling back to the default.
// If backend is empty but model contains "/" (e.g. "x-ai/grok-4"), it's an API model
// and we auto-detect the backend via resolveProvider.
func (o *Orchestrator) providerFor(backend, model string) provider.Provider {
	if backend == "" && strings.Contains(model, "/") {
		backend = "openrouter" // convention: slash in model name = API model
		log.Printf("[orchestrator] auto-detected backend=%s for model=%s", backend, model)
	}
	if o.resolveProvider != nil && backend != "" && backend != "cli" {
		return o.resolveProvider(backend)
	}
	return o.provider
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
	os.MkdirAll(taskDir, 0o775)
	os.Chmod(taskDir, 0o775)
	os.Chown(taskDir, 1000, 1000) // alf:alf so claude (gid 1000) can write

	log.Printf("[orchestrator] task %s started | teams=%d | message=%q", taskID, len(teams), truncate(userMessage, 120))

	meta := &TaskMeta{
		ID:        taskID,
		StartedAt: time.Now(),
		Status:    "running",
		Prompt:    userMessage,
		Source:    rc.Source,
		Team:      rc.Team,
	}

	// Persist team config and initial task state immediately.
	o.saveTeams(taskDir, teams)
	o.saveMeta(taskDir, meta)

	// Notify caller of the task ID so it can be referenced.
	if onProgress != nil {
		onProgress("task_started", taskID)
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
		ID:         taskID,
		StartedAt:  meta.StartedAt,
		Cancel:     cancel,
		Meta:       meta,
		ApprovalCh: make(chan ApprovalDecision, 1),
	}
	rt := o.running[taskID]
	o.mu.Unlock()
	defer func() {
		// If status is still "running" or "awaiting_approval" when we exit
		// (e.g. context cancelled, panic recovery), update disk so the task
		// isn't orphaned as invisible.
		if meta.Status == "running" || meta.Status == "awaiting_approval" {
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
	orchPrompt := BuildOrchestratorPrompt(teams, taskDir)
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
	consecutiveNonJSON := 0
	const maxConsecutiveNonJSON = 2
	lastRawOutput := ""

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
		log.Printf("[orchestrator] invoking model=%s backend=%s effort=%s resume=%v", orchModel, rc.Backend, orchEffort, hasResume)

		orchProvider := o.providerFor(rc.Backend, orchModel)

		params := provider.Params{
			Model:         orchModel,
			SystemPrompts: allSystemPrompts,
			ResumeID:      orchSessionID,
			DataDir:       taskDir,
			Effort:        orchEffort,
			MaxTurns:      orchMaxTurns,
			// No tools for orchestrator brain - it must only produce JSON delegation output.
			// Tools are for sub-agents, not the coordinator.
		}

		result, err := orchProvider.Invoke(ctx, prompt, params, nil)

		// Retry without resume if session expired.
		if err != nil && orchSessionID != "" && strings.Contains(err.Error(), "No conversation found") {
			log.Printf("[orchestrator] session expired, retrying without resume")
			sm.Clear(orchestratorKey)
			params.ResumeID = ""
			result, err = orchProvider.Invoke(ctx, prompt, params, nil)
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

		// Detect orchestrator turn limit - retry a limited number of times.
		if strings.Contains(result.Text, "Turn limit reached") {
			turnLimitRetries++
			if turnLimitRetries > maxTurnLimitRetries {
				log.Printf("[orchestrator] ✗ orchestrator hit turn limit %d times - aborting", turnLimitRetries)
				meta.Status = "failed"
				o.saveMeta(taskDir, meta)
				return "", meta, fmt.Errorf("orchestrator repeatedly hit turn limit (%d retries) - try increasing max_turns in the orchestrator tier config", maxTurnLimitRetries)
			}
			log.Printf("[orchestrator] ⚠ orchestrator hit turn limit (%d/%d retries) - clearing session", turnLimitRetries, maxTurnLimitRetries)
			sm.Clear(orchestratorKey)
			prompt = userMessage
			continue
		}

		// Parse orchestrator output.
		output := parseOrchestratorOutput(result.Text)

		// Plan output - display and optionally block for approval.
		if len(output.Plan) > 0 {
			meta.Plan = output.Plan
			o.saveMeta(taskDir, meta)
			if onProgress != nil {
				onProgress("plan_ready", "")
			}

			if rc.NeedValidation {
				meta.Status = "awaiting_approval"
				o.saveMeta(taskDir, meta)
				if onProgress != nil {
					onProgress("awaiting_approval", "")
				}
				// Block until user approves or context cancels.
				select {
				case <-ctx.Done():
					return "", meta, ctx.Err()
				case decision := <-rt.ApprovalCh:
					if !decision.Approved {
						meta.Status = "running"
						meta.ValidationFeedback = decision.Feedback
						meta.Plan = nil
						o.saveMeta(taskDir, meta)
						prompt = `{"plan_rejected":true,"feedback":"` + escapeJSON(decision.Feedback) + `","instruction":"revise your plan based on the feedback and output a new plan"}`
						continue
					}
					meta.Status = "running"
					o.saveMeta(taskDir, meta)
				}
			}
			// Tell orchestrator to execute the plan.
			prompt = `{"plan_approved":true,"instruction":"execute the plan now by delegating to agents"}`
			continue
		}

		// Final response - done.
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

		// No delegates and no response - treat as empty iteration.
		if len(output.Delegates) == 0 {
			// Detect repeated identical non-JSON output (e.g. model error loops).
			if result.Text == lastRawOutput {
				consecutiveNonJSON++
			} else {
				consecutiveNonJSON = 1
				lastRawOutput = result.Text
			}
			if consecutiveNonJSON >= maxConsecutiveNonJSON {
				log.Printf("[orchestrator] ✗ brain returned same non-JSON output %d times - aborting: %s",
					consecutiveNonJSON, truncate(result.Text, 200))
				meta.Status = "failed"
				o.saveMeta(taskDir, meta)
				return "", meta, fmt.Errorf("orchestrator brain error (repeated %d times): %s",
					consecutiveNonJSON, truncate(result.Text, 200))
			}
			log.Printf("[orchestrator] ⚠ no delegates and no response - nudging")
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
		agentResults := o.executeDelegates(ctx, output.Delegates, teams, sm, taskDir, meta, rc.SkillPrompts, rc.MemoryContext, onProgress)

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

		// Check if any agents failed.
		hasErrors := false
		for _, ar := range agentResults {
			if ar.Error != "" {
				hasErrors = true
				break
			}
		}

		reviewInstruction := "Review the agent results. If all results are satisfactory, produce your final response. If any result is incomplete, incorrect, or needs improvement, delegate again with corrective instructions."
		if hasErrors {
			reviewInstruction = "Some agents failed. Review the errors, decide if you need to retry with different instructions or if you can produce a final response with the successful results."
		}

		resumeData := struct {
			AgentResults []agentResultJSON `json:"agent_results"`
			Iteration    int               `json:"iteration"`
			TotalCostUSD float64           `json:"total_cost_usd"`
			Review       string            `json:"review_instruction"`
		}{
			AgentResults: resultsJSON,
			Iteration:    iteration + 2,
			TotalCostUSD: meta.TotalCost,
			Review:       reviewInstruction,
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
	skillPrompts []string,
	memoryContext []string,
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

	// Pre-register all agents as "working" in meta so the Tasks UI shows them immediately.
	mu.Lock()
	workingIndices := make(map[string]int, len(indexed)) // agent key → index in AgentCalls
	for _, id := range indexed {
		key := id.Agent
		if agentCount[id.Agent] > 1 {
			key = fmt.Sprintf("%s#%d", id.Agent, id.index)
		}
		workingIndices[key] = len(meta.AgentCalls)
		meta.AgentCalls = append(meta.AgentCalls, AgentResult{
			Agent:  id.Agent,
			Task:   id.DelegateRequest.Task,
			Status: "working",
		})
	}
	o.saveMeta(taskDir, meta)
	mu.Unlock()

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
			ar := o.invokeAgentWithKey(ctx, d, sessionKey, sm, taskDir, skillPrompts, memoryContext, onProgress)

			// Set final status.
			if ar.Error != "" {
				ar.Status = "failed"
			} else {
				ar.Status = "completed"
			}
			ar.Task = d.Task

			mu.Lock()
			results = append(results, ar)
			// Update the pre-registered "working" entry with final result.
			if i, ok := workingIndices[sessionKey]; ok && i < len(meta.AgentCalls) {
				meta.AgentCalls[i] = ar
			}
			meta.TotalCost += ar.CostUSD
			o.saveMeta(taskDir, meta)
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
	skillPrompts []string,
	memoryContext []string,
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

	// Resolve tier to get execution parameters.
	var tp TierParams
	if o.resolveTier != nil && ac.Tier != "" {
		var found bool
		tp, found = o.resolveTier(ac.Tier)
		if !found {
			log.Printf("[orchestrator] tier %q not found for agent %s/%s", ac.Tier, teamName, agentName)
			return AgentResult{
				Agent:    d.Agent,
				Error:    fmt.Sprintf("tier %q not found", ac.Tier),
				Duration: time.Since(start),
			}
		}
	} else {
		log.Printf("[orchestrator] agent %s/%s has no tier configured", teamName, agentName)
		return AgentResult{
			Agent:    d.Agent,
			Error:    fmt.Sprintf("agent %q has no tier configured", d.Agent),
			Duration: time.Since(start),
		}
	}

	if onProgress != nil {
		onProgress("agent", fmt.Sprintf("%s/%s", teamName, agentName))
	}

	// Every agent gets its own working directory under the task folder.
	agentDir := filepath.Join(taskDir, fmt.Sprintf("%s-%s", teamName, agentName))
	if sessionKey != d.Agent {
		agentDir = filepath.Join(taskDir, fmt.Sprintf("%s-%s-%s", teamName, agentName, sessionKey[strings.LastIndex(sessionKey, "#")+1:]))
	}
	os.MkdirAll(agentDir, 0o775)
	os.Chmod(agentDir, 0o775)
	os.Chown(agentDir, 1000, 1000) // alf:alf so claude (gid 1000) can write

	model := tp.Model

	sessionID := sm.Get(sessionKey)
	hasResume := sessionID != ""
	log.Printf("[orchestrator] → agent %s/%s: tier=%s model=%s backend=%s effort=%s write=%v max_turns=%d resume=%v",
		teamName, agentName, ac.Tier, model, tp.Backend, tp.Effort, tp.WriteCapable, tp.MaxTurns, hasResume)
	log.Printf("[orchestrator]   task: %s", truncate(d.Task, 150))

	// Build system prompts: tier prompt + agent's own prompt + memory context + skill prompts.
	sysPrompts := make([]string, 0, 2+len(memoryContext)+len(skillPrompts))
	if tp.SystemPrompt != "" {
		sysPrompts = append(sysPrompts, tp.SystemPrompt)
	}
	if ac.SystemPrompt != "" {
		sysPrompts = append(sysPrompts, ac.SystemPrompt)
	}
	sysPrompts = append(sysPrompts, memoryContext...)
	sysPrompts = append(sysPrompts, skillPrompts...)
	if len(memoryContext) > 0 || len(skillPrompts) > 0 {
		log.Printf("[orchestrator]   injecting %d memory + %d skill prompt(s) into agent %s/%s", len(memoryContext), len(skillPrompts), teamName, agentName)
	}

	params := provider.Params{
		Model:         model,
		Tools:         tp.Tools,
		WriteCapable:  tp.WriteCapable,
		Effort:        tp.Effort,
		MaxTurns:      tp.MaxTurns,
		SystemPrompts: sysPrompts,
		ResumeID:      sessionID,
		DataDir:       agentDir,
	}

	// Forward agent streaming events to progress callback.
	var agentProgress provider.OnProgress
	if onProgress != nil {
		agentProgress = func(event provider.StreamEvent) {
			switch event.Type {
			case "thinking":
				if event.Text == "" {
					onProgress("agent_thinking", agentName)
				}
			case "tool_use":
				onProgress("agent_tool", agentName+":"+event.Detail)
			}
		}
	}

	agentProv := o.providerFor(tp.Backend, model)

	// Wrap API provider with agentic tool loop when tier has tools.
	if o.toolRegistry != nil && o.toolExecutor != nil && len(tp.Tools) > 0 {
		if apiProv, ok := agentProv.(*provider.APIProvider); ok {
			o.toolRegistry.Rescan()
			// Expand tool wildcards into concrete tool names.
			resolvedTools := tp.Tools
			if len(resolvedTools) == 1 && resolvedTools[0] == "*" {
				resolvedTools = tooling.DiscoverToolNames(o.dataDir)
				resolvedTools = append(resolvedTools, o.toolRegistry.NativeToolNames()...)
				log.Printf("[orchestrator]   wildcard resolved to %d tools for %s/%s", len(resolvedTools), teamName, agentName)
			} else if len(resolvedTools) == 1 && resolvedTools[0] == "*native" {
				resolvedTools = o.toolRegistry.NativeToolNames()
				log.Printf("[orchestrator]   native wildcard resolved to %d tools for %s/%s", len(resolvedTools), teamName, agentName)
			}
			schemas := o.toolRegistry.ForToolsStrict(resolvedTools)
			if len(schemas) > 0 {
				tools := tooling.ToOpenAI(schemas)
				maxTurns := tp.MaxTurns
				if maxTurns <= 0 {
					maxTurns = 10
				}
				agentProv = provider.NewToolLoop(apiProv, &orchestratorToolAdapter{exec: o.toolExecutor}, tools, maxTurns)
				toolNames := make([]string, len(schemas))
				for i, s := range schemas {
					toolNames[i] = s.Name
				}
				params.SystemPrompts = append([]string{memory.ToolInstruction(toolNames)}, params.SystemPrompts...)
				log.Printf("[orchestrator]   tool loop enabled for %s/%s: %d tools, max_turns=%d", teamName, agentName, len(schemas), maxTurns)
			}
		}
	}

	result, err := agentProv.Invoke(ctx, d.Task, params, agentProgress)

	// Retry without resume if session expired.
	if err != nil && sessionID != "" && strings.Contains(err.Error(), "No conversation found") {
		log.Printf("[orchestrator]   agent %s/%s session expired, retrying", teamName, agentName)
		sm.Clear(sessionKey)
		params.ResumeID = ""
		result, err = agentProv.Invoke(ctx, d.Task, params, agentProgress)
	}

	dur := time.Since(start)
	if err != nil {
		log.Printf("[orchestrator] ✗ agent %s/%s failed after %dms: %v", teamName, agentName, dur.Milliseconds(), err)
		if onProgress != nil {
			onProgress("agent_done", fmt.Sprintf("%s ✗ (%ds)", agentName, int(dur.Seconds())))
		}
		return AgentResult{
			Agent:    d.Agent,
			Model:    model,
			Error:    err.Error(),
			Duration: dur,
		}
	}

	if result.SessionID != "" {
		sm.Set(sessionKey, result.SessionID)
	}

	log.Printf("[orchestrator] ← agent %s/%s: %dms $%.4f %d chars session=%s",
		teamName, agentName, dur.Milliseconds(), result.CostUSD, len(result.Text), truncate(result.SessionID, 12))

	// Detect turn limit as a sub-agent failure.
	if strings.Contains(result.Text, "Turn limit reached") {
		log.Printf("[orchestrator] ✗ agent %s/%s hit turn limit", teamName, agentName)
		if onProgress != nil {
			onProgress("agent_done", fmt.Sprintf("%s ✗ turn limit (%ds)", agentName, int(dur.Seconds())))
		}
		return AgentResult{
			Agent:    d.Agent,
			Model:    model,
			Text:     result.Text,
			Error:    "turn limit reached",
			CostUSD:  result.CostUSD,
			Duration: dur,
		}
	}

	if onProgress != nil {
		onProgress("agent_done", fmt.Sprintf("%s ✓ (%ds, $%.4f)", agentName, int(dur.Seconds()), result.CostUSD))
	}

	return AgentResult{
		Agent:    d.Agent,
		Model:    model,
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
		// Not valid JSON - do NOT treat as response; force re-delegation.
		log.Printf("[orchestrator] ⚠ output is not valid JSON, will nudge for proper delegation")
		return OrchestratorOutput{} // empty = triggers nudge loop
	}

	return out
}

// escapeJSON escapes a string for safe embedding in a JSON string literal.
func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	// Strip surrounding quotes.
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
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

// orchestratorToolAdapter bridges tooling.Executor to provider.ToolExecutor.
type orchestratorToolAdapter struct {
	exec *tooling.Executor
}

func (a *orchestratorToolAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
	result := a.exec.Execute(ctx, tooling.CallRequest{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	return provider.ToolCallResult{
		ID:      result.ID,
		Output:  result.Output,
		IsError: result.IsError,
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
