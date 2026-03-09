package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RunRecord captures one execution of a scheduled job.
type RunRecord struct {
	JobID      string        `json:"job_id"`
	JobName    string        `json:"job_name"`
	Tier       string        `json:"tier"`
	StartedAt  time.Time     `json:"started_at"`
	DurationMs int64         `json:"duration_ms"`
	Status     string        `json:"status"` // "ok", "error", "timeout", "skipped"
	Error      string        `json:"error,omitempty"`
	OutputLen  int           `json:"output_len"` // response length in chars
	CostUSD    float64       `json:"cost_usd,omitempty"`
	Model      string        `json:"model,omitempty"`
	NumTurns   int           `json:"num_turns,omitempty"`
	Iterations int           `json:"iterations,omitempty"` // orchestrator only
}

// RunLog provides append-only execution logging per job.
// Each job gets a JSONL file: logs/scheduler/{job-id}.jsonl
type RunLog struct {
	dir string
	mu  sync.Mutex
}

// NewRunLog creates a RunLog that stores files under dir.
func NewRunLog(dir string) *RunLog {
	return &RunLog{dir: dir}
}

// Append writes a record to the job's log file.
func (rl *RunLog) Append(rec RunRecord) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	os.MkdirAll(rl.dir, 0o755)

	path := filepath.Join(rl.dir, rec.JobID+".jsonl")
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
	path := filepath.Join(rl.dir, jobID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Parse all lines.
	var all []RunRecord
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec RunRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		all = append(all, rec)
	}

	// Return last N, newest first.
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	result := make([]RunRecord, limit)
	for i := 0; i < limit; i++ {
		result[i] = all[len(all)-1-i]
	}
	return result
}

// Stats computes summary statistics for a job.
func (rl *RunLog) Stats(jobID string) *RunStats {
	records := rl.Recent(jobID, 0) // all records (already newest-first)
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

// Cleanup removes log files for jobs that no longer exist.
func (rl *RunLog) Cleanup(activeIDs map[string]bool) {
	entries, err := os.ReadDir(rl.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 6 { // minimum: "x.jsonl"
			continue
		}
		id := name[:len(name)-6] // strip ".jsonl"
		if !activeIDs[id] {
			os.Remove(filepath.Join(rl.dir, name))
		}
	}
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

// Truncate keeps only the last N records for a job (prevents unbounded growth).
func (rl *RunLog) Truncate(jobID string, keep int) {
	records := rl.Recent(jobID, 0)
	if len(records) <= keep {
		return
	}

	// Recent returns newest-first; we need oldest-first for writing.
	toKeep := records[:keep]

	rl.mu.Lock()
	defer rl.mu.Unlock()

	path := filepath.Join(rl.dir, jobID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	// Write oldest first.
	for i := len(toKeep) - 1; i >= 0; i-- {
		data, _ := json.Marshal(toKeep[i])
		f.Write(data)
		f.WriteString("\n")
	}
}

// maxRecordsPerJob is the retention limit before auto-truncation.
const maxRecordsPerJob = 500

// appendAndTruncate appends a record and auto-truncates if needed.
func (rl *RunLog) appendAndTruncate(rec RunRecord) {
	rl.Append(rec)

	// Check file size as a proxy for record count (avoid parsing on every write).
	path := filepath.Join(rl.dir, rec.JobID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	// ~200 bytes per record × 500 = ~100KB. Truncate when file exceeds 150KB.
	if info.Size() > 150*1024 {
		rl.Truncate(rec.JobID, maxRecordsPerJob)
	}
}

// RecentAll returns the last N records across all jobs, newest first.
func (rl *RunLog) RecentAll(limit int) []RunRecord {
	entries, err := os.ReadDir(rl.dir)
	if err != nil {
		return nil
	}

	var all []RunRecord
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		id := e.Name()[:len(e.Name())-6]
		records := rl.Recent(id, 0)
		all = append(all, records...)
	}

	// Sort newest first.
	sortRecords(all)

	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all
}

// sortRecords sorts records by StartedAt descending (newest first).
func sortRecords(recs []RunRecord) {
	// Simple insertion sort — records are already mostly sorted per-file.
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
