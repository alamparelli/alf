package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RunRecord captures one execution of a scheduled job.
type RunRecord struct {
	JobID      string    `json:"job_id"`
	JobName    string    `json:"job_name"`
	Tier       string    `json:"tier"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	Status     string    `json:"status"` // "ok", "error", "timeout", "turn_limit", "skipped"
	Error      string    `json:"error,omitempty"`
	OutputLen  int       `json:"output_len"` // response length in chars
	CostUSD    float64   `json:"cost_usd,omitempty"`
	Model      string    `json:"model,omitempty"`
	NumTurns   int       `json:"num_turns,omitempty"`
	Iterations int       `json:"iterations,omitempty"` // orchestrator only
}

// retentionDays is how long daily log files are kept before auto-purge.
const retentionDays = 90

// RunLog provides append-only execution logging.
// All records for a given day go into a single file: logs/scheduler/{YYYY-MM-DD}.jsonl
type RunLog struct {
	dir       string
	mu        sync.Mutex
	lastPurge time.Time // avoid purging on every write
}

// NewRunLog creates a RunLog that stores daily files under dir.
func NewRunLog(dir string) *RunLog {
	return &RunLog{dir: dir}
}

// dailyFile returns the path for a given date's log file.
func (rl *RunLog) dailyFile(t time.Time) string {
	return filepath.Join(rl.dir, t.Format("2006-01-02")+".jsonl")
}

// Append writes a record to the daily log file.
func (rl *RunLog) Append(rec RunRecord) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	os.MkdirAll(rl.dir, 0o755)

	path := rl.dailyFile(rec.StartedAt)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f.Write(data)
	f.WriteString("\n")
}

// Recent returns the last N records for a job, newest first.
func (rl *RunLog) Recent(jobID string, limit int) []RunRecord {
	all := rl.readAll(func(r RunRecord) bool { return r.JobID == jobID })
	sortRecords(all)
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all
}

// Stats computes summary statistics for a job.
func (rl *RunLog) Stats(jobID string) *RunStats {
	records := rl.Recent(jobID, 0)
	if len(records) == 0 {
		return nil
	}

	stats := &RunStats{}
	var totalDur int64
	for _, r := range records {
		stats.TotalRuns++
		totalDur += r.DurationMs
		stats.TotalCost += r.CostUSD
		switch r.Status {
		case "ok":
			stats.OkCount++
		case "error", "timeout":
			stats.FailCount++
		case "skipped":
			stats.SkipCount++
		}
	}
	if stats.TotalRuns > 0 {
		stats.AvgDurationMs = totalDur / int64(stats.TotalRuns)
	}
	if len(records) > 0 {
		stats.LastStatus = records[0].Status
	}

	// Streak: consecutive successes or failures from latest.
	for _, r := range records {
		if r.Status == stats.LastStatus {
			stats.Streak++
		} else {
			break
		}
	}

	return stats
}

// RunStats summarizes a job's execution history.
type RunStats struct {
	TotalRuns     int     `json:"total_runs"`
	OkCount       int     `json:"ok_count"`
	FailCount     int     `json:"fail_count"`
	SkipCount     int     `json:"skip_count"`
	AvgDurationMs int64   `json:"avg_duration_ms"`
	TotalCost     float64 `json:"total_cost_usd,omitempty"`
	LastStatus    string  `json:"last_status"`
	Streak        int     `json:"streak"` // consecutive same-status runs
}

// Cleanup removes daily log files older than the retention period and
// also removes legacy per-job files (*.jsonl not matching YYYY-MM-DD pattern).
func (rl *RunLog) Cleanup(activeIDs map[string]bool) {
	rl.PurgeOld()
}

// PurgeOld removes daily log files older than retentionDays.
func (rl *RunLog) PurgeOld() {
	entries, err := os.ReadDir(rl.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(name, ".jsonl")
		d, err := time.Parse("2006-01-02", stem)
		if err != nil {
			// Legacy per-job file — remove it (data was migrated or is stale).
			os.Remove(filepath.Join(rl.dir, name))
			continue
		}
		if d.Before(cutoff) {
			os.Remove(filepath.Join(rl.dir, name))
		}
	}
}

// Truncate keeps only the last N records for a job.
// With daily files this trims across all day files.
func (rl *RunLog) Truncate(jobID string, keep int) {
	// Not needed with daily files + age-based purge.
	// Kept for API compatibility.
}

// maxRecordsPerJob is unused with daily files but kept for reference.
const maxRecordsPerJob = 500

// appendAndTruncate appends a record and periodically purges old daily files.
func (rl *RunLog) appendAndTruncate(rec RunRecord) {
	rl.Append(rec)

	// Purge old files at most once per day.
	rl.mu.Lock()
	shouldPurge := time.Since(rl.lastPurge) > 24*time.Hour
	if shouldPurge {
		rl.lastPurge = time.Now()
	}
	rl.mu.Unlock()

	if shouldPurge {
		rl.PurgeOld()
	}
}

// Since returns all records across all jobs started after the given time, newest first.
func (rl *RunLog) Since(since time.Time) []RunRecord {
	all := rl.readAll(func(r RunRecord) bool { return r.StartedAt.After(since) })
	sortRecords(all)
	return all
}

// DailyDigest generates a plain-text summary of job executions since the given time.
func (rl *RunLog) DailyDigest(since time.Time) string {
	records := rl.Since(since)
	if len(records) == 0 {
		return ""
	}

	// Aggregate per job.
	type jobSummary struct {
		name    string
		ok      int
		fail    int
		skip    int
		errors  []string
		totalMs int64
		cost    float64
	}
	byJob := make(map[string]*jobSummary)
	order := []string{}
	for _, r := range records {
		s, exists := byJob[r.JobID]
		if !exists {
			s = &jobSummary{name: r.JobName}
			byJob[r.JobID] = s
			order = append(order, r.JobID)
		}
		s.totalMs += r.DurationMs
		s.cost += r.CostUSD
		switch r.Status {
		case "ok":
			s.ok++
		case "error", "timeout":
			s.fail++
			if r.Error != "" {
				errMsg := r.Error
				if len(errMsg) > 120 {
					errMsg = errMsg[:120] + "..."
				}
				s.errors = append(s.errors, errMsg)
			}
		case "skipped":
			s.skip++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Schedule Report (%d runs since %s)\n\n",
		len(records), since.Format("15:04")))

	totalOk, totalFail := 0, 0
	for _, id := range order {
		s := byJob[id]
		totalOk += s.ok
		totalFail += s.fail
		status := "OK"
		if s.fail > 0 {
			status = fmt.Sprintf("FAIL %d/%d", s.fail, s.ok+s.fail+s.skip)
		}
		dur := time.Duration(s.totalMs) * time.Millisecond
		line := fmt.Sprintf("- %s: %s", s.name, status)
		if dur > time.Second {
			line += fmt.Sprintf(" (%s)", dur.Round(time.Second))
		}
		if s.cost > 0 {
			line += fmt.Sprintf(" $%.4f", s.cost)
		}
		sb.WriteString(line + "\n")
		for _, e := range s.errors {
			sb.WriteString(fmt.Sprintf("  err: %s\n", e))
		}
	}

	if totalFail == 0 {
		sb.WriteString(fmt.Sprintf("\nAll %d runs succeeded.", totalOk))
	} else {
		sb.WriteString(fmt.Sprintf("\n%d OK, %d failed.", totalOk, totalFail))
	}

	return sb.String()
}

// RecentAll returns the last N records across all jobs, newest first.
func (rl *RunLog) RecentAll(limit int) []RunRecord {
	all := rl.readAll(nil)
	sortRecords(all)
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all
}

// readAll reads all records from daily files, applying an optional filter.
func (rl *RunLog) readAll(filter func(RunRecord) bool) []RunRecord {
	entries, err := os.ReadDir(rl.dir)
	if err != nil {
		return nil
	}

	var all []RunRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rl.dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range splitLines(data) {
			if len(line) == 0 {
				continue
			}
			var rec RunRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				continue
			}
			if filter != nil && !filter(rec) {
				continue
			}
			all = append(all, rec)
		}
	}
	return all
}

// splitLines splits data by newline without allocating strings.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// sortRecords sorts records by StartedAt descending (newest first).
func sortRecords(recs []RunRecord) {
	// Simple insertion sort - records are already mostly sorted per-file.
	for i := 1; i < len(recs); i++ {
		for j := i; j > 0 && recs[j].StartedAt.After(recs[j-1].StartedAt); j-- {
			recs[j], recs[j-1] = recs[j-1], recs[j]
		}
	}
}

// FormattedDuration returns a human-readable duration string.
func (r RunRecord) FormattedDuration() string {
	d := time.Duration(r.DurationMs) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", r.DurationMs)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}
