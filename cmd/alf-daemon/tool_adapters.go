package main

// Adapters bridge subsystem types to tooling.Service interfaces.
// This avoids import cycles: tooling defines interfaces, adapters implement them here.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/alamparelli/alf/internal/agents"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
)

// --- Task adapter ---

type taskAdapter struct {
	orch         *agents.Orchestrator
	dataDir      string
	contextDir   string
	tierStore    cc.TierStore
	skillStore   skills.Store
	resolveModel func(short string) string
	onTaskEvent  func(taskID, status, summary string)
}

func (a *taskAdapter) Launch(ctx context.Context, opts tooling.TaskLaunchOpts) (string, error) {
	if a.orch == nil {
		return "", fmt.Errorf("orchestrator not available")
	}

	// Resolve agent tier config.
	rc := a.resolveAgentConfig(opts.Tier)

	orchPrep := agents.PrepareOrchestration(agents.OrchestrationInputs{
		UserMessage:          opts.Prompt,
		DataDir:              a.dataDir,
		ContextDir:           a.contextDir,
		Source:               "tool",
		Model:                rc.Model,
		Backend:              rc.Backend,
		Effort:               rc.Effort,
		MaxTurns:             rc.MaxTurns,
		OrchestratorMaxTurns: rc.OrchestratorMaxTurns,
		MaxIterations:        rc.MaxIterations,
		TimeoutMin:           rc.TimeoutMin,
		SkillStore:           a.skillStore,
		NeedValidation:       opts.NeedValidation,
		Team:                 opts.Team,
	})

	// Inject skill prompts if requested.
	if len(opts.Skills) > 0 {
		for _, name := range opts.Skills {
			if s, ok := a.skillStore.Get(strings.TrimSpace(name)); ok {
				orchPrep.Config.SkillPrompts = append(orchPrep.Config.SkillPrompts, s.Prompt)
			}
		}
	}

	var taskID string
	onProgress := func(phase, detail string) {
		if phase == "task_started" {
			taskID = detail
		}
		if a.onTaskEvent != nil && (phase == "awaiting_arbitration" || phase == "awaiting_approval") {
			a.onTaskEvent(taskID, phase, "")
		}
	}

	// Fire and forget — task is tracked by orchestrator.
	go func() {
		_, meta, err := a.orch.Run(context.Background(), opts.Prompt, orchPrep.SystemPrompts, orchPrep.Config, onProgress)
		if err != nil {
			log.Printf("[tool:task] background task failed: %v", err)
		}
		if a.onTaskEvent != nil && meta != nil {
			summary := meta.Response
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
			if meta.Status == "completed" || meta.Status == "failed" || meta.Status == "timeout" {
				a.onTaskEvent(meta.ID, meta.Status, summary)
			}
		}
	}()

	// Wait briefly for task_started callback to populate taskID.
	// If the orchestrator starts fast enough, we'll have it.
	// Otherwise return a placeholder.
	if taskID == "" {
		taskID = "(launching)"
	}
	return taskID, nil
}

func (a *taskAdapter) resolveAgentConfig(tierName string) agents.RunConfig {
	if a.tierStore == nil {
		return agents.RunConfig{}
	}
	tiers := a.tierStore.Current()
	target := "agent"
	if tierName != "" {
		target = tierName
	}
	for _, t := range tiers.Tiers {
		if t.Name == target {
			model := t.Model
			if (t.Backend == "" || t.Backend == "cli") && a.resolveModel != nil {
				model = a.resolveModel(t.Model)
			}
			return agents.RunConfig{
				Model:                model,
				Backend:              t.Backend,
				Effort:               t.Effort,
				MaxTurns:             t.MaxTurns,
				OrchestratorMaxTurns: t.OrchestratorMaxTurns,
				MaxIterations:        t.MaxIterations,
				TimeoutMin:           t.TimeoutMin,
			}
		}
	}
	return agents.RunConfig{}
}

func (a *taskAdapter) List() []tooling.TaskInfo {
	var result []tooling.TaskInfo

	// Running tasks from orchestrator.
	runningIDs := make(map[string]bool)
	if a.orch != nil {
		for _, rt := range a.orch.Running() {
			if rt.Meta != nil {
				runningIDs[rt.ID] = true
				result = append(result, taskMetaToInfo(rt.Meta))
			}
		}
	}

	// Completed tasks from disk.
	agentsDir := filepath.Join(a.dataDir, "agents")
	entries, _ := os.ReadDir(agentsDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		taskFile := filepath.Join(agentsDir, e.Name(), "task.json")
		data, err := os.ReadFile(taskFile)
		if err != nil {
			continue
		}
		var meta agents.TaskMeta
		if json.Unmarshal(data, &meta) != nil {
			continue
		}
		if runningIDs[meta.ID] {
			continue
		}
		result = append(result, taskMetaToInfo(&meta))
	}
	return result
}

func (a *taskAdapter) Cancel(id string) bool {
	if a.orch == nil {
		return false
	}
	return a.orch.Cancel(id)
}

func (a *taskAdapter) Delete(id string) bool {
	if a.orch == nil {
		return false
	}
	return a.orch.DeleteTask(id)
}

func (a *taskAdapter) Approve(id string, approved bool, feedback string) bool {
	if a.orch == nil {
		return false
	}
	return a.orch.Approve(id, agents.ApprovalDecision{Approved: approved, Feedback: feedback})
}

func taskMetaToInfo(m *agents.TaskMeta) tooling.TaskInfo {
	ti := tooling.TaskInfo{
		ID:         m.ID,
		Prompt:     m.Prompt,
		Status:     m.Status,
		StartedAt:  m.StartedAt.Format("2006-01-02T15:04:05Z"),
		Team:       m.Team,
		Iterations: m.Iterations,
	}
	if m.Status == "completed" || m.Status == "failed" {
		resp := m.Response
		if len(resp) > 500 {
			resp = resp[:500] + "..."
		}
		ti.Response = resp
	}
	return ti
}

// --- Team adapter ---

type teamAdapter struct {
	store   agents.Store
	dataDir string
}

func (a *teamAdapter) All() []tooling.TeamInfo {
	teams := a.store.All()
	result := make([]tooling.TeamInfo, 0, len(teams))
	for _, t := range teams {
		result = append(result, teamConfigToInfo(t))
	}
	return result
}

func (a *teamAdapter) Get(name string) (*tooling.TeamInfo, bool) {
	t, ok := a.store.Get(name)
	if !ok {
		return nil, false
	}
	info := teamConfigToInfo(t)
	return &info, true
}

func (a *teamAdapter) Save(req tooling.TeamSaveRequest) error {
	agentConfigs := make([]agents.AgentConfig, len(req.Agents))
	for i, ag := range req.Agents {
		agentConfigs[i] = agents.AgentConfig{
			Name:        ag.Name,
			Description: ag.Description,
			Tier:        ag.Tier,
			Skills:      ag.Skills,
		}
	}
	tc := &agents.TeamConfig{
		Name:        req.Name,
		Description: req.Description,
		Agents:      agentConfigs,
	}
	// Write to disk.
	teamsDir := filepath.Join(a.dataDir, "agents", "teams")
	os.MkdirAll(teamsDir, 0o755)

	// Check if team already exists (update) or new (create).
	existing, _ := a.store.Get(req.Name)
	if existing != nil {
		tc.ID = existing.ID
	}

	data, err := json.MarshalIndent(tc, "", "  ")
	if err != nil {
		return err
	}

	fileName := tc.Name + ".json"
	if tc.ID != "" {
		fileName = tc.ID + ".json"
	}
	if err := os.WriteFile(filepath.Join(teamsDir, fileName), data, 0o644); err != nil {
		return err
	}
	return a.store.Reload()
}

func (a *teamAdapter) Delete(nameOrID string) error {
	t, ok := a.store.Get(nameOrID)
	if !ok {
		return fmt.Errorf("team %q not found", nameOrID)
	}
	teamsDir := filepath.Join(a.dataDir, "agents", "teams")
	// Try by ID, then by name.
	for _, name := range []string{t.ID + ".json", t.Name + ".json"} {
		path := filepath.Join(teamsDir, name)
		if _, err := os.Stat(path); err == nil {
			os.Remove(path)
			a.store.Reload()
			return nil
		}
	}
	return fmt.Errorf("team file not found for %q", nameOrID)
}

func teamConfigToInfo(t *agents.TeamConfig) tooling.TeamInfo {
	agents := make([]tooling.AgentInfo, len(t.Agents))
	for i, a := range t.Agents {
		agents[i] = tooling.AgentInfo{
			Name:        a.Name,
			Description: a.Description,
			Tier:        a.Tier,
			Skills:      a.Skills,
		}
	}
	return tooling.TeamInfo{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Agents:      agents,
	}
}

// --- Skill adapter ---

type skillAdapter struct {
	store     skills.Store
	skillsDir string // system skills directory
	dataDir   string
}

func (a *skillAdapter) All() []tooling.SkillInfo {
	allSkills := a.store.All()
	result := make([]tooling.SkillInfo, 0, len(allSkills))
	for _, s := range allSkills {
		source := "system"
		if strings.HasPrefix(s.Dir, filepath.Join(a.dataDir, "skills")) {
			source = "user"
		}
		result = append(result, tooling.SkillInfo{
			Name:        s.Name,
			Description: s.Description,
			Triggers:    s.Triggers,
			Tier:        s.Tier,
			Source:      source,
		})
	}
	return result
}

func (a *skillAdapter) Get(name string) (*tooling.SkillDetail, bool) {
	s, ok := a.store.Get(name)
	if !ok {
		return nil, false
	}
	source := "system"
	if strings.HasPrefix(s.Dir, filepath.Join(a.dataDir, "skills")) {
		source = "user"
	}
	return &tooling.SkillDetail{
		SkillInfo: tooling.SkillInfo{
			Name:        s.Name,
			Description: s.Description,
			Triggers:    s.Triggers,
			Tier:        s.Tier,
			Source:      source,
		},
		Content: s.Prompt,
	}, true
}

// --- App/Marketplace adapter ---

type appAdapter struct {
	appStore    cc.AppStore
	marketplace *marketplace.Manager
}

func (a *appAdapter) List() []tooling.AppInfo {
	var result []tooling.AppInfo

	// Marketplace apps (with state).
	if a.marketplace != nil {
		for _, app := range a.marketplace.List() {
			result = append(result, tooling.AppInfo{
				Name:        app.Slug,
				DisplayName: app.Name,
				Icon:        app.Icon,
				Description: app.Description,
				State:       string(app.State),
			})
		}
	}

	// Directory apps (from apps/).
	if a.appStore != nil {
		metas, _ := a.appStore.List()
		mpSlugs := make(map[string]bool, len(result))
		for _, a := range result {
			mpSlugs[a.Name] = true
		}
		for _, m := range metas {
			if mpSlugs[m.Name] {
				continue // already listed from marketplace
			}
			result = append(result, tooling.AppInfo{
				Name:        m.Name,
				DisplayName: m.DisplayName,
				Icon:        m.Icon,
				Description: m.Description,
				State:       "enabled",
			})
		}
	}
	return result
}

func (a *appAdapter) Catalog() ([]tooling.RemoteAppInfo, error) {
	if a.marketplace == nil {
		return nil, fmt.Errorf("marketplace not available")
	}
	remote, err := a.marketplace.FetchCatalog()
	if err != nil {
		return nil, err
	}
	result := make([]tooling.RemoteAppInfo, len(remote))
	for i, r := range remote {
		result[i] = tooling.RemoteAppInfo{
			Slug:        r.Slug,
			Name:        r.Name,
			Version:     r.Version,
			Description: r.Description,
			Category:    r.Category,
		}
	}
	return result, nil
}

func (a *appAdapter) Install(slug string) error {
	if a.marketplace == nil {
		return fmt.Errorf("marketplace not available")
	}
	return a.marketplace.Install(slug)
}

func (a *appAdapter) Update(slug string) error {
	if a.marketplace == nil {
		return fmt.Errorf("marketplace not available")
	}
	return a.marketplace.Update(slug)
}

func (a *appAdapter) Enable(slug string) error {
	if a.marketplace == nil {
		return fmt.Errorf("marketplace not available")
	}
	return a.marketplace.Enable(slug)
}

func (a *appAdapter) Disable(slug string) error {
	if a.marketplace == nil {
		return fmt.Errorf("marketplace not available")
	}
	return a.marketplace.Disable(slug)
}

func (a *appAdapter) Uninstall(slug string) error {
	if a.marketplace == nil {
		return fmt.Errorf("marketplace not available")
	}
	return a.marketplace.Uninstall(slug)
}

// --- Config adapter ---

type configAdapter struct {
	store cc.ConfigStore
}

func (a *configAdapter) Get() (map[string]any, error) {
	cfg, err := a.store.Load()
	if err != nil {
		return nil, err
	}
	// Marshal to JSON and back to get a clean map (redacting secrets).
	data, _ := json.Marshal(cfg)
	var result map[string]any
	json.Unmarshal(data, &result)
	// Redact backend API keys if present.
	if backends, ok := result["backends"].(map[string]any); ok {
		for _, v := range backends {
			if bc, ok := v.(map[string]any); ok {
				delete(bc, "api_key")
				delete(bc, "vault_service")
			}
		}
	}
	return result, nil
}

// --- Tier adapter ---

type tierAdapter struct {
	store cc.TierStore
}

func (a *tierAdapter) List() []tooling.TierInfo {
	tc := a.store.Current()
	if tc == nil {
		return nil
	}
	result := make([]tooling.TierInfo, 0, len(tc.Tiers))
	for _, t := range tc.Tiers {
		result = append(result, tooling.TierInfo{
			Name:        t.Name,
			Model:       t.Model,
			Backend:     t.Backend,
			Enabled:     t.Enabled,
			Routable:    t.Routable,
			Tools:       t.Tools,
			Effort:      t.Effort,
			Description: t.Description,
		})
	}
	return result
}

// --- Log adapter ---

type logAdapter struct {
	reader cc.LogReader
}

func (a *logAdapter) Available() []string {
	return a.reader.Available()
}

func (a *logAdapter) Tail(name string, lines int) ([]string, error) {
	return a.reader.Tail(name, lines)
}

// --- Search adapter ---

type searchAdapter struct {
	appStore    cc.AppStore
	marketplace *marketplace.Manager
	dataDir     string
}

func (a *searchAdapter) Search(query string, types []string) ([]tooling.SearchResult, error) {
	q := strings.ToLower(query)
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}
	searchAll := len(typeSet) == 0

	var results []tooling.SearchResult

	// Search apps.
	if searchAll || typeSet["apps"] {
		if a.appStore != nil {
			metas, _ := a.appStore.List()
			for _, m := range metas {
				if matchesQuery(q, m.Name, m.DisplayName, m.Description) {
					results = append(results, tooling.SearchResult{
						Type: "app",
						Name: m.DisplayName,
						Desc: m.Description,
					})
				}
			}
		}
		if a.marketplace != nil {
			for _, app := range a.marketplace.List() {
				if matchesQuery(q, app.Slug, app.Name, app.Description) {
					results = append(results, tooling.SearchResult{
						Type: "app",
						Name: app.Name,
						Desc: app.Description,
					})
				}
			}
		}
	}

	// Search files.
	if searchAll || typeSet["files"] {
		count := 0
		filepath.Walk(a.dataDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || count >= 20 {
				return filepath.SkipDir
			}
			name := info.Name()
			if strings.HasPrefix(name, ".") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(a.dataDir, path)
			if matchesQuery(q, name, rel) {
				results = append(results, tooling.SearchResult{
					Type: "file",
					Name: name,
					Path: rel,
				})
				count++
			}
			return nil
		})
	}

	return results, nil
}

func matchesQuery(q string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// --- LLM invoke adapter ---

type llmAdapter struct {
	tierStore        cc.TierStore
	providerRegistry *provider.Registry
	resolveModel     func(short string) string
	dataDir          string
}

func (a *llmAdapter) Invoke(ctx context.Context, opts tooling.LLMInvokeOpts) (string, error) {
	if a.tierStore == nil {
		return "", fmt.Errorf("tier store not available")
	}

	// Find the tier config.
	tiers := a.tierStore.Current()
	var found *cc.Tier
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == opts.Tier && tiers.Tiers[i].Enabled {
			found = &tiers.Tiers[i]
			break
		}
	}
	if found == nil {
		// List available tiers in the error for discoverability.
		var names []string
		for _, t := range tiers.Tiers {
			if t.Enabled {
				names = append(names, t.Name)
			}
		}
		return "", fmt.Errorf("tier %q not found or disabled (available: %s)", opts.Tier, strings.Join(names, ", "))
	}

	// Resolve model name.
	model := found.Model
	backend := found.Backend
	if (backend == "" || backend == "cli") && a.resolveModel != nil {
		model = a.resolveModel(found.Model)
	}

	// Get provider for the backend.
	prov := a.providerRegistry.ForBackend(backend)

	// Build params.
	params := provider.Params{
		Model:   model,
		DataDir: a.dataDir,
		Effort:  found.Effort,
	}
	if found.MaxTurns > 0 {
		params.MaxTurns = found.MaxTurns
	} else {
		params.MaxTurns = 3 // reasonable default for one-shot
	}

	// Inject system prompt if provided.
	if opts.System != "" {
		params.SystemPrompts = []string{opts.System}
	}

	result, err := prov.Invoke(ctx, opts.Prompt, params, nil)
	if err != nil {
		return "", fmt.Errorf("LLM invocation failed: %w", err)
	}

	return result.Text, nil
}

