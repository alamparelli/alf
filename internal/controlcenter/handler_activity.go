package controlcenter

import (
	"net/http"
	"time"

	"github.com/alamparelli/alf/internal/agents"
)

// ActivityItem represents a single active operation in the system.
type ActivityItem struct {
	Type      string `json:"type"`                 // "chat", "schedule", "task"
	Name      string `json:"name"`                 // human-readable label
	StartedAt string `json:"started_at,omitempty"` // RFC3339
	Elapsed   string `json:"elapsed,omitempty"`    // human-readable duration
}

// ActivityHandler returns the list of currently active operations.
// GET /api/activity → { "items": [...], "count": N }
type ActivityHandler struct {
	ChatService  *ChatService
	Scheduler    ScheduleEngine
	Orchestrator *agents.Orchestrator
}

func (h *ActivityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	var items []ActivityItem

	// 1. Active chat jobs.
	if h.ChatService != nil {
		for _, job := range h.ChatService.ActiveJobs() {
			_ = job
			items = append(items, ActivityItem{
				Type: "chat",
				Name: "Chat response",
			})
		}
	}

	// 2. Running scheduled jobs.
	if h.Scheduler != nil {
		for _, j := range h.Scheduler.List(false) {
			if j.Running {
				items = append(items, ActivityItem{
					Type: "schedule",
					Name: j.Name,
				})
			}
		}
	}

	// 3. Running orchestrator tasks.
	if h.Orchestrator != nil {
		for _, t := range h.Orchestrator.Running() {
			elapsed := time.Since(t.StartedAt).Truncate(time.Second).String()
			item := ActivityItem{
				Type:      "task",
				Name:      "Agent task",
				StartedAt: t.StartedAt.Format(time.RFC3339),
				Elapsed:   elapsed,
			}
			if t.Meta != nil && t.Meta.Prompt != "" {
				label := t.Meta.Prompt
				if len(label) > 60 {
					label = label[:57] + "..."
				}
				item.Name = label
			}
			items = append(items, item)
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
	})
}
