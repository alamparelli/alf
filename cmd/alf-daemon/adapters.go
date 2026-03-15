package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/memstore"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/router"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
)

// extractorAdapter bridges provider.CLIProvider to memstore.ExtractorProvider,
// with optional fallback to an API backend when CLI is unavailable.
type extractorAdapter struct {
	prov     *provider.CLIProvider
	registry *provider.Registry // optional: fallback to API backend
}

func (a *extractorAdapter) Invoke(ctx context.Context, prompt string, params memstore.ExtractorParams) (string, error) {
	// Try API backend first if registry has backends (avoids spawning CLI process).
	if a.registry != nil {
		names := a.registry.BackendNames()
		if len(names) > 0 {
			apiProv := a.registry.ForBackend(names[0])
			model := params.Model
			// CLI model names need "anthropic/" prefix for API backends.
			if !strings.Contains(model, "/") {
				model = "anthropic/" + model
			}
			result, err := apiProv.Invoke(ctx, prompt, provider.Params{
				Model:    model,
				MaxTurns: params.MaxTurns,
				DataDir:  params.DataDir,
			}, nil)
			if err == nil {
				return result.Text, nil
			}
			log.Printf("memstore: API extraction failed (%v), falling back to CLI", err)
		}
	}

	// Fallback to CLI provider.
	result, err := a.prov.Invoke(ctx, prompt, provider.Params{
		Model:    params.Model,
		MaxTurns: params.MaxTurns,
		DataDir:  params.DataDir,
		Tools:    []string{""}, // explicit empty to disable all tools
	}, nil)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// memStoreRecaller adapts memstore.Store to the cc.MemoryRecaller interface.
type memStoreRecaller struct {
	store *memstore.Store
}

func (r *memStoreRecaller) Search(query string, limit int) ([]cc.MemoryResult, error) {
	results, err := r.store.Search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]cc.MemoryResult, len(results))
	for i, m := range results {
		out[i] = cc.MemoryResult{Text: m.Text, Type: m.Type, Distance: m.Distance}
	}
	return out, nil
}

// schedulerProvider adapts provider.CLIProvider to the scheduler.ProviderInvoker interface.
type schedulerProvider struct {
	p *provider.CLIProvider
}

func (s *schedulerProvider) Invoke(ctx context.Context, prompt string, params scheduler.ProviderParams, onProgress interface{}) (*scheduler.ProviderResult, error) {
	pp := provider.Params{
		Model:         params.Model,
		Tools:         params.Tools,
		WriteCapable:  params.WriteCapable,
		Effort:        params.Effort,
		SystemPrompts: params.SystemPrompts,
		MaxTurns:      params.MaxTurns,
		DataDir:       params.DataDir,
	}
	result, err := s.p.Invoke(ctx, prompt, pp, nil)
	if err != nil {
		return nil, err
	}
	return &scheduler.ProviderResult{
		SessionID: result.SessionID,
		Text:      result.Text,
		Model:     result.Model,
		CostUSD:   result.CostUSD,
		NumTurns:  result.NumTurns,
	}, nil
}

// schedulerTierStore adapts cc.TierStore to the scheduler.TierStoreReader interface.
type schedulerTierStore struct {
	ts cc.TierStore
}

func (s *schedulerTierStore) Current() *scheduler.TiersSnapshot {
	tc := s.ts.Current()
	if tc == nil {
		return nil
	}
	snap := &scheduler.TiersSnapshot{
		Tiers: make([]scheduler.TierInfo, len(tc.Tiers)),
	}
	for i, t := range tc.Tiers {
		snap.Tiers[i] = scheduler.TierInfo{
			Name:         t.Name,
			Model:        router.ResolveModel(t.Model),
			Tools:        t.Tools,
			WriteCapable: t.WriteCapable,
			Effort:       t.Effort,
			MaxTurns:     t.MaxTurns,
		}
	}
	return snap
}

// schedulerChatLogger adapts cc.ChatStore to the scheduler.ChatLogger interface.
type schedulerChatLogger struct {
	store *cc.ChatStore
}

func (l *schedulerChatLogger) LogScheduledMessage(text, tier, jobName string) {
	l.store.Append(cc.ChatMessage{
		ID:        cc.NewMessageID(),
		Role:      "assistant",
		Text:      text,
		Tier:      tier,
		Timestamp: time.Now(),
		SessionID: "scheduled:" + jobName,
	})
}

// schedulerCCNotifier pushes schedule notifications to the Control Center chat.
type schedulerCCNotifier struct {
	store *cc.ChatStore
}

func (n *schedulerCCNotifier) Notify(text string) {
	n.store.Append(cc.ChatMessage{
		ID:        cc.NewMessageID(),
		Role:      "assistant",
		Text:      text,
		Tier:      "scheduler",
		Timestamp: time.Now(),
		SessionID: "scheduler:notification",
	})
}

// schedulerSkillStore adapts skills.Store for the scheduler's SkillStoreReader interface.
type schedulerSkillStore struct {
	s skills.Store
}

func (a *schedulerSkillStore) Get(name string) (*scheduler.SkillInfo, bool) {
	sk, ok := a.s.Get(name)
	if !ok {
		return nil, false
	}
	return &scheduler.SkillInfo{Name: sk.Name, Prompt: sk.Prompt}, true
}

// schedulerOrchestrator adapts agents.Orchestrator to the scheduler.OrchestratorRunner interface.
type schedulerOrchestrator struct {
	o *agents.Orchestrator
}

func (s *schedulerOrchestrator) Run(ctx context.Context, userMessage string, systemPrompts []string, rc scheduler.RunConfig, onProgress scheduler.ProgressFunc) (string, *scheduler.TaskMeta, error) {
	var agentProgress agents.ProgressFunc
	if onProgress != nil {
		agentProgress = agents.ProgressFunc(onProgress)
	}

	text, meta, err := s.o.Run(ctx, userMessage, systemPrompts, agents.RunConfig{
		Model:                rc.Model,
		Backend:              rc.Backend,
		Effort:               rc.Effort,
		MaxIterations:        rc.MaxIterations,
		MaxTurns:             rc.MaxTurns,
		OrchestratorMaxTurns: rc.OrchestratorMaxTurns,
	}, agentProgress)
	if err != nil {
		return "", nil, err
	}

	return text, &scheduler.TaskMeta{
		Iterations: meta.Iterations,
		TotalCost:  meta.TotalCost,
		Status:     meta.Status,
	}, nil
}

// ccScheduleAdapter adapts scheduler.Engine to the cc.ScheduleEngine interface.
type ccScheduleAdapter struct {
	engine *scheduler.Engine
}

func (a *ccScheduleAdapter) List(userOnly bool) []cc.ScheduleJob {
	if a.engine == nil {
		return nil
	}
	jobs := a.engine.List(userOnly)
	out := make([]cc.ScheduleJob, len(jobs))
	for i, j := range jobs {
		out[i] = schedulerJobToCC(j)
	}
	return out
}

func (a *ccScheduleAdapter) Create(name, schedule, tier, prompt, command, output string, timeout time.Duration, skills []string) (*cc.ScheduleJob, error) {
	j, err := a.engine.Create(name, schedule, tier, prompt, command, output, timeout, skills)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func (a *ccScheduleAdapter) CreateReminder(name, schedule, message, output string, timeout time.Duration) (*cc.ScheduleJob, error) {
	j, err := a.engine.CreateReminder(name, schedule, message, output, timeout)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func (a *ccScheduleAdapter) Delete(id string) error {
	return a.engine.Delete(id)
}

func (a *ccScheduleAdapter) RunNow(id string) error {
	return a.engine.RunNow(id)
}

func (a *ccScheduleAdapter) Update(id string, fields map[string]string) (*cc.ScheduleJob, error) {
	j, err := a.engine.Update(id, fields)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func schedulerJobToCC(j *scheduler.Job) cc.ScheduleJob {
	sj := cc.ScheduleJob{
		ID:         j.ID,
		Name:       j.Name,
		Schedule:   j.Schedule,
		Tier:       j.Tier,
		Prompt:     j.Prompt,
		Command:    j.Command,
		Message:    j.Message,
		Output:     j.Output,
		Enabled:    j.Enabled,
		System:     j.System,
		Managed:    j.Managed,
		AutoDelete: j.AutoDelete,
		Skills:     j.Skills,
		CreatedAt:  j.CreatedAt.Format(time.RFC3339),
	}
	if j.Timeout > 0 {
		sj.Timeout = j.Timeout.String()
	}
	if j.LastRun != nil {
		sj.LastRun = j.LastRun.Format(time.RFC3339)
	}
	if j.NextRun != nil {
		sj.NextRun = j.NextRun.Format(time.RFC3339)
	}
	sj.LastError = j.LastError
	sj.Running = j.IsRunning()
	return sj
}

// tgToolExecutorAdapter bridges tooling.Executor to provider.ToolExecutor for the TG handler.
type tgToolExecutorAdapter struct {
	exec *tooling.Executor
}

func (a *tgToolExecutorAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
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
