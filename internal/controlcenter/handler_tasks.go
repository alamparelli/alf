package controlcenter

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/skills"
)

// TasksHandler serves running and completed agent tasks, and launches new ones.
// Task launches bypass ChatService entirely - they run in their own goroutine
// and are tracked by the Orchestrator, not the chat mutex.
type TasksHandler struct {
	Orchestrator *agents.Orchestrator
	DataDir      string
	ContextDir   string

	// Optional dependencies for building orchestrator context.
	TierStore    TierStore
	SkillStore   skills.Store
	Recaller     MemoryRecaller
	ResolveModel func(short string) string
}

func (h *TasksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.launch(w, r)
	case http.MethodDelete:
		h.cancel(w, r)
	default:
		methodNotAllowed(w)
	}
}

// launch starts an orchestrator task in a separate goroutine, completely
// independent of the chat pipeline. Multiple tasks can run concurrently.
func (h *TasksHandler) launch(w http.ResponseWriter, r *http.Request) {
	if h.Orchestrator == nil {
		http.Error(w, "agent not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Message        string `json:"message"`
		NeedValidation bool   `json:"need_validation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}

	// Build orchestrator system prompts (same logic as chat_service agent path).
	var sysPrompts []string
	if h.Recaller != nil {
		if block := recallMemories(h.Recaller, req.Message); block != "" {
			sysPrompts = append(sysPrompts, block)
		}
	}

	var skillInjections []string
	if h.SkillStore != nil {
		if catalog := skills.BuildCatalog(h.SkillStore); catalog != "" {
			sysPrompts = append(sysPrompts, catalog)
		}
		if matched := skills.MatchTriggers(h.SkillStore, req.Message); len(matched) > 0 {
			for _, sk := range matched {
				if sk.Prompt != "" {
					skillInjections = append(skillInjections, sk.Prompt)
				}
			}
		}
	}

	// Resolve agent tier params from tier config.
	rc := h.resolveAgentConfig()
	rc.SkillPrompts = skillInjections
	rc.MemoryContext = memory.CollectAgentContext(h.ContextDir)
	rc.NeedValidation = req.NeedValidation

	// Fire and forget - task is tracked by orchestrator.running map.
	go func() {
		_, _, err := h.Orchestrator.Run(context.Background(), req.Message, sysPrompts, rc, nil)
		if err != nil {
			log.Printf("[tasks] background task failed: %v", err)
		}
	}()

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resolveAgentConfig reads the "agent" tier config to get model/effort/timeout settings.
func (h *TasksHandler) resolveAgentConfig() agents.RunConfig {
	if h.TierStore == nil {
		return agents.RunConfig{}
	}
	tiers := h.TierStore.Current()
	for _, t := range tiers.Tiers {
		if t.Name == "agent" {
			model := t.Model
			if (t.Backend == "" || t.Backend == "cli") && h.ResolveModel != nil {
				model = h.ResolveModel(t.Model)
			}
			return agents.RunConfig{
				Model:                model,
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

func (h *TasksHandler) list(w http.ResponseWriter, r *http.Request) {
	// Get running tasks from agent.
	var running []agents.TaskMeta
	runningIDs := make(map[string]bool)
	if h.Orchestrator != nil {
		for _, rt := range h.Orchestrator.Running() {
			if rt.Meta != nil {
				running = append(running, *rt.Meta)
				runningIDs[rt.ID] = true
			}
		}
	}

	// Load recent completed tasks from disk (agents/*/task.json).
	var completed []agents.TaskMeta
	agentsDir := filepath.Join(h.DataDir, "agents")
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
		// Skip tasks that are actually running in memory right now.
		if runningIDs[meta.ID] {
			continue
		}
		// Orphaned "running" or "awaiting_approval" tasks (on disk but not in memory)
		// were interrupted by a daemon restart - mark them accordingly.
		if meta.Status == "running" || meta.Status == "awaiting_approval" {
			meta.Status = "interrupted"
		}
		completed = append(completed, meta)
	}

	// Sort completed by start time descending, keep last 20.
	sort.Slice(completed, func(i, j int) bool {
		return completed[i].StartedAt.After(completed[j].StartedAt)
	})
	if len(completed) > 20 {
		completed = completed[:20]
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"running":   running,
		"completed": completed,
	})
}

func (h *TasksHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if h.Orchestrator == nil {
		http.Error(w, "agent not available", http.StatusServiceUnavailable)
		return
	}
	ok := h.Orchestrator.Cancel(id)
	respondJSON(w, http.StatusOK, map[string]any{"cancelled": ok})
}

// TaskApproveHandler handles approval/rejection of orchestrator plans.
type TaskApproveHandler struct {
	Orchestrator *agents.Orchestrator
}

func (h *TaskApproveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.Orchestrator == nil {
		http.Error(w, "agent not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	ok := h.Orchestrator.Approve(req.ID, agents.ApprovalDecision{
		Approved: req.Approved,
		Feedback: req.Feedback,
	})
	respondJSON(w, http.StatusOK, map[string]any{"ok": ok})
}
