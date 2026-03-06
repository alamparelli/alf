package controlcenter

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/alamparelli/alf/internal/agents"
)

// TasksHandler serves running and completed orchestrator tasks.
type TasksHandler struct {
	Orchestrator *agents.Orchestrator
	DataDir      string
}

func (h *TasksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodDelete:
		h.cancel(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *TasksHandler) list(w http.ResponseWriter, r *http.Request) {
	// Get running tasks from orchestrator.
	var running []agents.TaskMeta
	if h.Orchestrator != nil {
		for _, rt := range h.Orchestrator.Running() {
			if rt.Meta != nil {
				running = append(running, *rt.Meta)
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
		// Only include non-running tasks (running ones come from orchestrator).
		if meta.Status == "running" {
			continue
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

	json.NewEncoder(w).Encode(map[string]any{
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
		http.Error(w, "orchestrator not available", http.StatusServiceUnavailable)
		return
	}
	ok := h.Orchestrator.Cancel(id)
	json.NewEncoder(w).Encode(map[string]any{"cancelled": ok})
}
