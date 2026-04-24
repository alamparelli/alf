package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	agents "github.com/alamparelli/alf/internal/runtime/agents"
	"github.com/alamparelli/alf/internal/runtime/comms"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	firewall "github.com/alamparelli/alf/internal/sandbox/network"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/curation"
	provider "github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
)

// extractorAdapter bridges provider.CLIProvider to curation.ExtractorProvider,
// with optional fallback to an API backend when CLI is unavailable.
type extractorAdapter struct {
	prov      *provider.CLIProvider
	registry  *provider.Registry // optional: fallback to API backend
	tierStore cc.TierStore       // read memory.extract_backend preference
}

func (a *extractorAdapter) Invoke(ctx context.Context, prompt string, params curation.ExtractorParams) (string, error) {
	// Check tier profile for explicit extract_backend preference.
	// Default: use the router backend/model (cheap, fast, already configured).
	var forceBackend, forceModel string
	if tc := a.tierStore.Current(); tc != nil {
		if tc.Memory != nil && tc.Memory.ExtractBackend != "" {
			forceBackend = tc.Memory.ExtractBackend
			forceModel = tc.Memory.ExtractModel
		} else if tc.RouterBackend != "" {
			forceBackend = tc.RouterBackend
			forceModel = tc.RouterModel
		}
	}

	model := params.Model
	if forceModel != "" {
		model = forceModel
	} else if forceBackend != "" && forceBackend != "cli" {
		// Backend set but no model — use router model so we don't send
		// an incompatible fallback model (e.g. claude-haiku to codex).
		if tc := a.tierStore.Current(); tc != nil && tc.RouterModel != "" {
			model = tc.RouterModel
		}
	}

	// If set to "cli" or not configured, use CLI directly (no API fallback cascade).
	if forceBackend == "" || forceBackend == "cli" {
		return a.invokeCLI(ctx, prompt, model, params)
	}

	// If a specific backend is configured, use only that one.
	if a.registry != nil && a.registry.HasBackend(forceBackend) {
		apiProv := a.registry.ForBackend(forceBackend)
		apiModel := model
		if !strings.Contains(apiModel, "/") && forceBackend != "codex" {
			apiModel = "anthropic/" + apiModel
		}
		result, err := apiProv.Invoke(ctx, prompt, provider.Params{
			Model: apiModel, MaxTurns: params.MaxTurns, DataDir: params.DataDir,
		}, nil)
		if err == nil {
			return result.Text, nil
		}
		log.Printf("memstore: extraction via %s failed (%v), falling back to CLI", forceBackend, err)
		// Use a CLI-compatible model for fallback — the forced model may be codex-only.
		cliModel := params.Model // original model from resolveModel()
		return a.invokeCLI(ctx, prompt, cliModel, params)
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

func (a *extractorAdapter) invokeCLI(ctx context.Context, prompt, model string, params curation.ExtractorParams) (string, error) {
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

// memoryScopes lists the known memory types produced by the extractor and
// consolidator. Recallers fan out a Search across each because the
// memory.Store contract searches one Scope at a time — the old memstore
// model put every memory in a single table so a one-shot search sufficed.
// When a new memType is introduced (e.g. in extractor.go's type whitelist),
// add it here too.
var memoryScopes = []memory.Scope{"fact", "preference", "decision", "contact", "summary"}

// searchMemoryAcrossScopes fans out a Search across every known memory
// scope and returns the top `limit` hits merged by descending score.
// Shared by the cc and comms recaller adapters below.
func searchMemoryAcrossScopes(store memory.Store, query string, limit int) ([]memory.Hit, error) {
	if store == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var all []memory.Hit
	for _, scope := range memoryScopes {
		hits, err := store.Search(ctx, scope, query, limit)
		if err != nil {
			// One bad scope must not black-hole the recall — log and keep
			// going with the others. Recall is best-effort.
			log.Printf("memory: recall scope=%q failed: %v", scope, err)
			continue
		}
		// Tag each hit with its scope so the adapter can return the Type
		// field MemoryResult consumers expect.
		for _, h := range hits {
			if h.Document.Metadata == nil {
				h.Document.Metadata = map[string]string{}
			}
			h.Document.Metadata["scope"] = string(scope)
			all = append(all, h)
		}
	}
	// Merge-sort by score descending (insertion sort — N stays small).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].Score > all[j-1].Score; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// memoryCCRecaller adapts memory.Store to the cc.MemoryRecaller interface.
// Replaces the pre-#337 *memstore.Store adapter once the documents table
// is fed by the dual-write shim (sub-ticket C1).
type memoryCCRecaller struct {
	store memory.Store
}

func (r *memoryCCRecaller) Search(query string, limit int) ([]cc.MemoryResult, error) {
	hits, err := searchMemoryAcrossScopes(r.store, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]cc.MemoryResult, len(hits))
	for i, h := range hits {
		// memory.Hit.Score is similarity (higher == more relevant);
		// MemoryResult.Distance is cosine distance (lower == more relevant).
		// Invert so downstream ranking code keeps working.
		out[i] = cc.MemoryResult{
			Text:     h.Document.Text,
			Type:     h.Document.Metadata["scope"],
			Distance: float64(1 - h.Score),
		}
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
			Fallback:             t.Fallback,
			Role:                 t.Role,
		}
	}
	return snap
}

// memoryIngestAdapter implements cc.MemoryStorer on top of memory.Store so
// the /api/memory/ingest writer path can land on the unified store
// (#337c4a). Preserves the legacy Store() signature so handler_memory.go
// and its tests need no change.
//
// Doc ID strategy: derive from a SHA-256 prefix of the text so repeated
// ingests of identical content upsert-refresh a single row rather than
// accumulate copies. This is coarser than memstore's FTS5 fuzzy dedup —
// it only catches byte-identical duplicates — but it keeps the natural
// "idempotent re-ingest" behaviour that users rely on.
type memoryIngestAdapter struct {
	store memory.Store
}

func (a *memoryIngestAdapter) Store(text, memType, source string, meta map[string]any) (int64, error) {
	if a.store == nil {
		return 0, fmt.Errorf("memory: ingest adapter has no backing store")
	}
	h := sha256.Sum256([]byte(text))
	docID := fmt.Sprintf("ingest-%x", h[:12])
	mm := map[string]string{
		"source":     source,
		"created_at": time.Now().Format(time.RFC3339),
	}
	for k, v := range meta {
		switch vv := v.(type) {
		case string:
			mm[k] = vv
		default:
			if b, err := json.Marshal(v); err == nil {
				mm[k] = string(b)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.store.Index(ctx, memory.Scope(memType), memory.Document{
		ID: docID, Text: text, Metadata: mm,
	}); err != nil {
		return 0, err
	}
	return 0, nil
}

// memoryCommsRecaller adapts memory.Store to the comms.MemoryRecaller
// interface. Symmetric to memoryCCRecaller but returns comms.MemoryResult.
type memoryCommsRecaller struct {
	store memory.Store
}

func (r *memoryCommsRecaller) Search(query string, limit int) ([]comms.MemoryResult, error) {
	hits, err := searchMemoryAcrossScopes(r.store, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]comms.MemoryResult, len(hits))
	for i, h := range hits {
		out[i] = comms.MemoryResult{
			Text:     h.Document.Text,
			Type:     h.Document.Metadata["scope"],
			Distance: float64(1 - h.Score),
		}
	}
	return out, nil
}

// schedulerProvider adapts provider.Registry to the scheduler.ProviderInvoker interface.
// It routes each job invocation to the correct backend (CLI, API, Codex, …) based on
// the Backend field resolved from the tier config.
type schedulerProvider struct {
	r *provider.Registry
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
	p := s.r.ForBackend(params.Backend)
	result, err := p.Invoke(ctx, prompt, pp, nil)
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
		model := resolveModel(t.Model)
		if model == "" {
			model = t.Model // preserve non-Claude models (e.g. gpt-5.4)
		}
		snap.Tiers[i] = scheduler.TierInfo{
			Name:         t.Name,
			Backend:      t.Backend,
			Model:        model,
			Tools:        t.Tools,
			WriteCapable: t.WriteCapable,
			Effort:       t.Effort,
			MaxTurns:     t.MaxTurns,
			Role:         t.Role,
		}
	}
	return snap
}

// schedulerChatLogger adapts memory.Store to the scheduler.ChatLogger interface.
type schedulerChatLogger struct {
	mem memory.Store
}

func (l *schedulerChatLogger) LogScheduledMessage(text, tier, jobName string) {
	if l.mem == nil {
		return
	}
	ctx := context.Background()
	_ = l.mem.EnsureConv(ctx, "_scheduler", "", "scheduler")
	_, _ = l.mem.AppendMessage(ctx, "_scheduler", memory.Message{
		Role:      "assistant",
		Channel:   "scheduler",
		Content:   text,
		Blocks:    []memory.ContentBlock{{Type: memory.BlockText, Text: text}},
		Tier:      tier,
		SessionID: "scheduled:" + jobName,
	})
}

// schedulerCCNotifier pushes schedule notifications to the Control Center chat
// and emits an SSE event so the frontend can show a toast/sound.
type schedulerCCNotifier struct {
	mem    memory.Store
	broker *cc.EventBroker
}

func (n *schedulerCCNotifier) Notify(text string) {
	if n.mem == nil {
		return
	}
	ctx := context.Background()
	convID, _ := n.mem.LatestConvID(ctx, "cc")
	if convID == "" {
		convID = "_system"
		_ = n.mem.EnsureConv(ctx, convID, "", "cc")
	}
	_, _ = n.mem.AppendMessage(ctx, convID, memory.Message{
		Role:      "assistant",
		Channel:   "scheduler",
		Content:   text,
		Blocks:    []memory.ContentBlock{{Type: memory.BlockText, Text: text}},
		Tier:      "scheduler",
		SessionID: "scheduler:notification",
	})
	if n.broker != nil {
		// Truncate for the SSE payload (toast preview).
		preview := text
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		n.broker.EmitWithData(cc.EventNewMessage, preview)
	}
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

func (a *ccScheduleAdapter) Create(name, schedule, tier, prompt, command, output string, timeout time.Duration, skills []string, reason string) (*cc.ScheduleJob, error) {
	j, err := a.engine.Create(name, schedule, tier, prompt, command, output, timeout, skills, reason)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func (a *ccScheduleAdapter) CreateReminder(name, schedule, message, output string, timeout time.Duration, reason string) (*cc.ScheduleJob, error) {
	j, err := a.engine.CreateReminder(name, schedule, message, output, timeout, reason)
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
		ID:          j.ID,
		Name:        j.Name,
		Description: j.Description,
		Reason:      j.Reason,
		Schedule:    j.Schedule,
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

// firewallToolAdapter adapts firewall.Proxy to tooling.FirewallService.
type firewallToolAdapter struct {
	proxy *firewall.Proxy
	store *firewall.Store
}

func (a *firewallToolAdapter) RecentEntries(limit int) []tooling.FirewallEntry {
	if a.proxy == nil {
		return nil
	}
	raw := a.proxy.Log.Entries()
	// Return the last N entries.
	if len(raw) > limit {
		raw = raw[len(raw)-limit:]
	}
	out := make([]tooling.FirewallEntry, len(raw))
	for i, e := range raw {
		out[i] = tooling.FirewallEntry{
			Time:    e.Time,
			Method:  e.Method,
			Host:    e.Host,
			Path:    e.Path,
			Blocked: e.Blocked,
			Source:  e.Source,
		}
	}
	return out
}

func (a *firewallToolAdapter) Hosts() []tooling.FirewallHostStat {
	if a.store == nil {
		return nil
	}
	raw := a.store.Hosts()
	out := make([]tooling.FirewallHostStat, len(raw))
	for i, h := range raw {
		out[i] = tooling.FirewallHostStat{
			Host:    h.Host,
			Count:   h.Count,
			Allowed: h.Allowed,
			Blocked: h.Blocked,
			Vault:   h.Vault,
		}
	}
	return out
}

