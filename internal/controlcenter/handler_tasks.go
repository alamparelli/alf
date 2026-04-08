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

	// OnTaskEvent is called when a task reaches a terminal or user-attention state.
	// Arguments: source (cc/tg), taskID, status (completed/failed/awaiting_arbitration), summary.
	OnTaskEvent func(source, taskID, status, summary string)

	EventBroker *EventBroker
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
		respondError(w, http.StatusServiceUnavailable, "agent not available")
		return
	}

	var req struct {
		Message        string `json:"message"`
		Team           string `json:"team"`
		NeedValidation bool   `json:"need_validation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Message == "" {
		respondError(w, http.StatusBadRequest, "empty message")
		return
	}

	// Resolve agent tier params and build orchestration inputs.
	agentRC := h.resolveAgentConfig()
	var recallBlock string
	if h.Recaller != nil {
		recallBlock = recallMemories(h.Recaller, req.Message)
	}

	orchPrep := agents.PrepareOrchestration(agents.OrchestrationInputs{
		UserMessage:          req.Message,
		DataDir:              h.DataDir,
		ContextDir:           h.ContextDir,
		Source:               "chat",
		Model:                agentRC.Model,
		Backend:              agentRC.Backend,
		Effort:               agentRC.Effort,
		MaxTurns:             agentRC.MaxTurns,
		OrchestratorMaxTurns: agentRC.OrchestratorMaxTurns,
		MaxIterations:        agentRC.MaxIterations,
		TimeoutMin:           agentRC.TimeoutMin,
		RecallBlock:          recallBlock,
		SkillStore:           h.SkillStore,
		NeedValidation:       req.NeedValidation,
		Team:                 req.Team,
	})

	// Progress callback: emit events for arbitration.
	var taskID string
	onProgress := func(phase, detail string) {
		if phase == "task_started" {
			taskID = detail
		}
		if h.OnTaskEvent != nil && (phase == "awaiting_arbitration" || phase == "awaiting_approval") {
			h.OnTaskEvent("cc", taskID, phase, "")
		}
	}

	// Fire and forget - task is tracked by orchestrator.running map.
	go func() {
		if h.EventBroker != nil {
			h.EventBroker.Emit(EventTasks)
		}
		_, meta, err := h.Orchestrator.Run(context.Background(), req.Message, orchPrep.SystemPrompts, orchPrep.Config, onProgress)
		if err != nil {
			log.Printf("[tasks] background task failed: %v", err)
		}
		// Notify on terminal state.
		if h.OnTaskEvent != nil && meta != nil {
			summary := meta.Response
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
			if meta.Status == "completed" || meta.Status == "failed" || meta.Status == "timeout" {
				h.OnTaskEvent("cc", meta.ID, meta.Status, summary)
			}
		}
		if h.EventBroker != nil {
			h.EventBroker.Emit(EventTasks)
		}
	}()

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resolveAgentConfig reads the orchestrator tier config to get model/effort/timeout settings.
func (h *TasksHandler) resolveAgentConfig() agents.RunConfig {
	if h.TierStore == nil {
		return agents.RunConfig{}
	}
	tiers := h.TierStore.Current()
	for _, t := range tiers.Tiers {
		if t.IsOrchestrator() {
			model := t.Model
			if (t.Backend == "" || t.Backend == "cli") && h.ResolveModel != nil {
				model = h.ResolveModel(t.Model)
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
		// Orphaned tasks (on disk but not in memory) were interrupted by a daemon restart.
		if meta.Status == "running" || meta.Status == "awaiting_approval" || meta.Status == "awaiting_arbitration" {
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
		respondError(w, http.StatusBadRequest, "missing id")
		return
	}
	if h.Orchestrator == nil {
		respondError(w, http.StatusServiceUnavailable, "agent not available")
		return
	}

	// action=delete removes the task from disk (completed only).
	if r.URL.Query().Get("action") == "delete" {
		ok := h.Orchestrator.DeleteTask(id)
		if ok && h.EventBroker != nil {
			h.EventBroker.Emit(EventTasks)
		}
		respondJSON(w, http.StatusOK, map[string]any{"deleted": ok})
		return
	}

	ok := h.Orchestrator.Cancel(id)
	if ok && h.EventBroker != nil {
		h.EventBroker.Emit(EventTasks)
	}
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
		respondError(w, http.StatusServiceUnavailable, "agent not available")
		return
	}
	var req struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.ID == "" {
		respondError(w, http.StatusBadRequest, "missing id")
		return
	}
	ok := h.Orchestrator.Approve(req.ID, agents.ApprovalDecision{
		Approved: req.Approved,
		Feedback: req.Feedback,
	})
	respondJSON(w, http.StatusOK, map[string]any{"ok": ok})
}
