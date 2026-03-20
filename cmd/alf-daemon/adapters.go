package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/comms"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/memstore"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/router"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/skills"
)

// extractorAdapter bridges provider.CLIProvider to memstore.ExtractorProvider,
// with optional fallback to an API backend when CLI is unavailable.
type extractorAdapter struct {
	prov      *provider.CLIProvider
	registry  *provider.Registry // optional: fallback to API backend
	tierStore cc.TierStore       // read memory.extract_backend preference
}

func (a *extractorAdapter) Invoke(ctx context.Context, prompt string, params memstore.ExtractorParams) (string, error) {
	// Check tier profile for explicit extract_backend preference.
	var forceBackend, forceModel string
	if tc := a.tierStore.Current(); tc != nil && tc.Memory != nil {
		forceBackend = tc.Memory.ExtractBackend
		forceModel = tc.Memory.ExtractModel
	}

	model := params.Model
	if forceModel != "" {
		model = forceModel
	}

	// If explicitly set to "cli", skip API backends entirely.
	if forceBackend == "cli" {
		return a.invokeCLI(ctx, prompt, model, params)
	}

	// If a specific backend is configured, use only that one.
	if forceBackend != "" && a.registry != nil && a.registry.HasBackend(forceBackend) {
		apiProv := a.registry.ForBackend(forceBackend)
		apiModel := model
		if !strings.Contains(apiModel, "/") {
			apiModel = "anthropic/" + apiModel
		}
		result, err := apiProv.Invoke(ctx, prompt, provider.Params{
			Model: apiModel, MaxTurns: params.MaxTurns, DataDir: params.DataDir,
		}, nil)
		if err == nil {
			return result.Text, nil
		}
		log.Printf("memstore: extraction via %s failed (%v), falling back to CLI", forceBackend, err)
		return a.invokeCLI(ctx, prompt, model, params)
	}

	// Auto mode: try API backends (prefer authenticated over local), fallback CLI.
	if a.registry != nil {
		names := a.registry.BackendNames()
		preferred := make([]string, 0, len(names))
		local := make([]string, 0)
		for _, n := range names {
			if ap := a.registry.GetAPIBackend(n); ap != nil && !ap.IsOllamaCompat() {
				preferred = append(preferred, n)
			} else {
				local = append(local, n)
			}
		}
		ordered := append(preferred, local...)
		for _, name := range ordered {
			apiProv := a.registry.ForBackend(name)
			apiModel := model
			if !strings.Contains(apiModel, "/") {
				apiModel = "anthropic/" + apiModel
			}
			result, err := apiProv.Invoke(ctx, prompt, provider.Params{
				Model: apiModel, MaxTurns: params.MaxTurns, DataDir: params.DataDir,
			}, nil)
			if err == nil {
				return result.Text, nil
			}
			log.Printf("memstore: API extraction via %s failed (%v), trying next", name, err)
		}
	}

	return a.invokeCLI(ctx, prompt, model, params)
}

func (a *extractorAdapter) invokeCLI(ctx context.Context, prompt, model string, params memstore.ExtractorParams) (string, error) {
	result, err := a.prov.Invoke(ctx, prompt, provider.Params{
		Model:    model,
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

// commsTierStore adapts cc.TierStore to the comms.TierStoreReader interface.
type commsTierStore struct {
	ts cc.TierStore
}

func (c *commsTierStore) Snapshot() comms.TiersSnapshot {
	cur := c.ts.Current()
	snap := comms.TiersSnapshot{
		DefaultFallback: cur.DefaultFallback,
		Tiers:           make([]comms.TierInfo, len(cur.Tiers)),
	}
	for i, t := range cur.Tiers {
		snap.Tiers[i] = comms.TierInfo{
			Name:                 t.Name,
			Model:                t.Model,
			Priority:             t.Priority,
			Enabled:              t.Enabled,
			Routable:             t.Routable,
			ForceCommand:         t.ForceCommand,
			Tools:                t.Tools,
			WriteCapable:         t.WriteCapable,
			Effort:               t.Effort,
			MaxTurns:             t.MaxTurns,
			OrchestratorMaxTurns: t.OrchestratorMaxTurns,
			MaxIterations:        t.MaxIterations,
			TimeoutMin:           t.TimeoutMin,
			Backend:              t.Backend,
			SystemPrompt:         t.SystemPrompt,
			RouterLabel:          t.RouterLabel,
			ContextWeight:        t.EffectiveContextWeight(),
		}
	}
	return snap
}

// commsRecaller adapts memstore.Store to the comms.MemoryRecaller interface.
type commsRecaller struct {
	store *memstore.Store
}

func (r *commsRecaller) Search(query string, limit int) ([]comms.MemoryResult, error) {
	results, err := r.store.Search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]comms.MemoryResult, len(results))
	for i, m := range results {
		out[i] = comms.MemoryResult{Text: m.Text, Type: m.Type, Distance: m.Distance}
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

	source := rc.Source
	if source == "" {
		source = "schedule"
	}
	text, meta, err := s.o.Run(ctx, userMessage, systemPrompts, agents.RunConfig{
		Model:                rc.Model,
		Backend:              rc.Backend,
		Effort:               rc.Effort,
		MaxIterations:        rc.MaxIterations,
		MaxTurns:             rc.MaxTurns,
		OrchestratorMaxTurns: rc.OrchestratorMaxTurns,
		Source:               source,
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

