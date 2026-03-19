package controlcenter

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

// ChatHandler handles POST /api/chat, GET /api/chat (history), DELETE /api/chat (new session).
type ChatHandler struct {
	Service *ChatService
}

func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.sendMessage(w, r)
	case http.MethodGet:
		h.history(w, r)
	case http.MethodDelete:
		h.newSession(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *ChatHandler) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Message == "" && len(req.MediaIDs) == 0 {
		http.Error(w, `{"error":"message or media_ids required"}`, http.StatusBadRequest)
		return
	}

	job := h.Service.StartJob(req)
	streamJob(w, r, job, 0)
}

func (h *ChatHandler) newSession(w http.ResponseWriter, r *http.Request) {
	onboard := r.URL.Query().Get("onboard") == "1"
	old, newConvID := h.Service.NewSession(onboard)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "previous_session": old, "conv_id": newConvID})
}

func (h *ChatHandler) history(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	var before time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		if t, err := time.Parse(time.RFC3339, b); err == nil {
			before = t
		}
	}

	convID := r.URL.Query().Get("conv_id")
	msgs := h.Service.History(limit, before, convID)
	respondJSON(w, http.StatusOK, msgs)
}

// ChatConversationsHandler handles GET /api/chat/conversations.
type ChatConversationsHandler struct {
	Service *ChatService
}

func (h *ChatConversationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		convs := h.Service.Conversations()
		// Also include the current active conv_id.
		currentConvID := h.Service.CurrentConvID()
		respondJSON(w, http.StatusOK, map[string]any{
			"conversations":  convs,
			"active_conv_id": currentConvID,
		})
		return
	}
	methodNotAllowed(w)
}

// ChatSkillsHandler handles GET /api/chat/skills (list) and DELETE /api/chat/skills (clear).
type ChatSkillsHandler struct {
	Service *ChatService
}

func (h *ChatSkillsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		skills := h.Service.ActiveSkills()
		respondJSON(w, http.StatusOK, map[string]any{"skills": skills})
	case http.MethodDelete:
		h.Service.ClearActiveSkills()
		respondJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

// ChatJobHandler handles GET /api/chat/job (status + reconnect) and DELETE (cancel).
type ChatJobHandler struct {
	Service *ChatService
}

func (h *ChatJobHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Reconnect to stream: /api/chat/job?stream=<id>&offset=<n>
		if jobID := r.URL.Query().Get("stream"); jobID != "" {
			job := h.Service.GetJob(jobID)
			if job == nil {
				http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
				return
			}
			offset := 0
			if o := r.URL.Query().Get("offset"); o != "" {
				if n, err := strconv.Atoi(o); err == nil {
					offset = n
				}
			}
			streamJob(w, r, job, offset)
			return
		}

		// Status check - scoped by conv_id.
		convID := r.URL.Query().Get("conv_id")
		j := h.Service.ActiveJob(convID)
		if j == nil {
			respondJSON(w, http.StatusOK, map[string]any{"active": false})
		} else {
			respondJSON(w, http.StatusOK, map[string]any{
				"active": true,
				"job_id": j.ID,
				"events": j.eventCount(),
			})
		}

	case http.MethodDelete:
		convID := r.URL.Query().Get("conv_id")
		j := h.Service.ActiveJob(convID)
		if j != nil {
			j.cancel()
		}
		respondJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		methodNotAllowed(w)
	}
}

// streamJob writes SSE events from a background job to the HTTP response.
// The job continues running even if the client disconnects.
func streamJob(w http.ResponseWriter, r *http.Request, job *chatJob, offset int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// First event: job ID for reconnection.
	jobData, _ := json.Marshal(map[string]string{"job_id": job.ID})
	fmt.Fprintf(w, "event: job\ndata: %s\n\n", jobData)
	flusher.Flush()

	ctx := r.Context()
	for {
		events, done, jobErr, wait := job.snapshot(offset)

		for _, evt := range events {
			data, err := json.Marshal(evt.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
			offset++
		}

		if done {
			if jobErr != nil {
				log.Printf("[chat] job %s error: %v", job.ID, jobErr)
				errData, _ := json.Marshal(map[string]string{"error": jobErr.Error()})
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
				flusher.Flush()
			}
			return
		}

		select {
		case <-ctx.Done():
			log.Printf("[chat-job] client disconnected, job %s continues in background", job.ID)
			return
		case <-wait:
			// New events available.
		}
	}
}
