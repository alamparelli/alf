package controlcenter

import (
	"net/http"
	"strconv"

	"github.com/alamparelli/alf/internal/scheduler"
)

// ScheduleLogsHandler serves run history for scheduled jobs.
type ScheduleLogsHandler struct {
	RunLog *scheduler.RunLog // nil = not available
}

func (h *ScheduleLogsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.RunLog == nil {
		respondError(w, http.StatusServiceUnavailable, "run logs not available")
		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	jobID := r.URL.Query().Get("id")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	// If job ID specified: return records + stats for that job.
	if jobID != "" {
		records := h.RunLog.Recent(jobID, limit)
		stats := h.RunLog.Stats(jobID)
		respondJSON(w, http.StatusOK, map[string]any{
			"records": records,
			"stats":   stats,
		})
		return
	}

	// No job ID: return recent records across all jobs.
	records := h.RunLog.RecentAll(limit)
	respondJSON(w, http.StatusOK, map[string]any{
		"records": records,
	})
}
