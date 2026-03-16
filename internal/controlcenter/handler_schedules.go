package controlcenter

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// ScheduleRunHandler triggers an immediate one-shot execution of a job.
type ScheduleRunHandler struct {
	Engine ScheduleEngine
}

func (h *ScheduleRunHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Engine == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduler not available"})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.ID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}

	if err := h.Engine.RunNow(req.ID); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SchedulesHandler serves GET/POST/PUT/DELETE for scheduled jobs.
type SchedulesHandler struct {
	Engine ScheduleEngine
}

func (h *SchedulesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Engine == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduler not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		jobs := h.Engine.List(false)
		respondJSON(w, http.StatusOK, map[string]any{"jobs": jobs})

	case http.MethodPost:
		var req struct {
			Name     string   `json:"name"`
			Schedule string   `json:"schedule"`
			Tier     string   `json:"tier"`
			Prompt   string   `json:"prompt"`
			Command  string   `json:"command"`
			Message  string   `json:"message"`
			Output   string   `json:"output"`
			Timeout  string   `json:"timeout"`
			Skills   []string `json:"skills"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[schedules] POST decode error: %v", err)
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if req.Name == "" || req.Schedule == "" {
			log.Printf("[schedules] POST missing fields: name=%q schedule=%q", req.Name, req.Schedule)
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "name and schedule are required"})
			return
		}
		var timeout time.Duration
		if req.Timeout != "" {
			var err error
			timeout, err = time.ParseDuration(req.Timeout)
			if err != nil {
				respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid timeout: " + err.Error()})
				return
			}
		}
		// Reminder mode: message is mutually exclusive with prompt/command/tier.
		if req.Message != "" {
			if req.Prompt != "" || req.Command != "" || req.Tier != "" {
				respondJSON(w, http.StatusBadRequest, map[string]string{"error": "message is a direct push notification - cannot be combined with prompt, command, or tier"})
				return
			}
			job, err := h.Engine.CreateReminder(req.Name, req.Schedule, req.Message, req.Output, timeout)
			if err != nil {
				respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			respondJSON(w, http.StatusCreated, map[string]any{"job": job})
			return
		}
		job, err := h.Engine.Create(req.Name, req.Schedule, req.Tier, req.Prompt, req.Command, req.Output, timeout, req.Skills)
		if err != nil {
			log.Printf("[schedules] POST create error: %v (name=%q schedule=%q tier=%q)", err, req.Name, req.Schedule, req.Tier)
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		respondJSON(w, http.StatusCreated, map[string]any{"job": job})

	case http.MethodPut:
		var req struct {
			ID     string            `json:"id"`
			Fields map[string]string `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if req.ID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
			return
		}
		job, err := h.Engine.Update(req.ID, req.Fields)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"job": job})

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "id query param required"})
			return
		}
		if err := h.Engine.Delete(id); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		methodNotAllowed(w)
	}
}
