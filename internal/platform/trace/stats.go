package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// ToolStat holds aggregate metrics for a single tool over a window.
type ToolStat struct {
	Tool     string  `json:"tool"`
	Runs     int     `json:"runs"`
	Errors   int     `json:"errors"`
	ErrRate  float64 `json:"err_rate"`
	AvgMs    float64 `json:"avg_ms"`
	P95Ms    int64   `json:"p95_ms"`
	MaxMs    int64   `json:"max_ms"`
	LastErr  string  `json:"last_error,omitempty"`
}

// ToolStatsReport is the aggregated view written to disk / returned by callers.
type ToolStatsReport struct {
	GeneratedAt time.Time  `json:"generated_at"`
	WindowDays  int        `json:"window_days"`
	From        time.Time  `json:"from"`
	To          time.Time  `json:"to"`
	TotalRuns   int        `json:"total_runs"`
	TotalErrors int        `json:"total_errors"`
	Tools       []ToolStat `json:"tools"` // sorted by errors desc, then runs desc
}

// AggregateToolStats walks logs/traces/*.jsonl for the last `days` days and
// returns aggregated tool execution metrics. `days <= 0` defaults to 7.
func AggregateToolStats(dataDir string, days int) (*ToolStatsReport, error) {
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	from := now.AddDate(0, 0, -days+1)
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())

	dir := filepath.Join(dataDir, "logs", "traces")
	type accum struct {
		runs, errors int
		durations    []int64
		lastErr      string
	}
	byTool := make(map[string]*accum)

	for d := 0; d < days; d++ {
		day := from.AddDate(0, 0, d)
		path := filepath.Join(dir, day.Format("2006-01-02")+".jsonl")
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			var t Tracer
			if err := json.Unmarshal(sc.Bytes(), &t); err != nil {
				continue // skip malformed lines, don't abort the whole aggregation
			}
			for _, s := range t.Spans {
				if s.Name != "tool_exec" {
					continue
				}
				name := s.Tags["tool"]
				if name == "" {
					name = "(unknown)"
				}
				a := byTool[name]
				if a == nil {
					a = &accum{}
					byTool[name] = a
				}
				a.runs++
				a.durations = append(a.durations, s.DurationMs)
				if s.Tags["is_error"] == "true" {
					a.errors++
					if msg := s.Tags["error"]; msg != "" {
						a.lastErr = msg
					}
				}
				// Treat non-zero exit_code as an error too, in case is_error
				// was not set by the caller.
				if ec, err := strconv.Atoi(s.Tags["exit_code"]); err == nil && ec != 0 && s.Tags["is_error"] != "true" {
					a.errors++
				}
			}
		}
		f.Close()
	}

	report := &ToolStatsReport{
		GeneratedAt: now,
		WindowDays:  days,
		From:        from,
		To:          now,
	}
	for tool, a := range byTool {
		stat := ToolStat{Tool: tool, Runs: a.runs, Errors: a.errors, LastErr: a.lastErr}
		if a.runs > 0 {
			stat.ErrRate = float64(a.errors) / float64(a.runs)
		}
		if len(a.durations) > 0 {
			var sum int64
			var maxMs int64
			for _, d := range a.durations {
				sum += d
				if d > maxMs {
					maxMs = d
				}
			}
			stat.AvgMs = math.Round(float64(sum)/float64(len(a.durations))*100) / 100
			stat.MaxMs = maxMs
			stat.P95Ms = percentile(a.durations, 0.95)
		}
		report.Tools = append(report.Tools, stat)
		report.TotalRuns += a.runs
		report.TotalErrors += a.errors
	}

	sort.Slice(report.Tools, func(i, j int) bool {
		if report.Tools[i].Errors != report.Tools[j].Errors {
			return report.Tools[i].Errors > report.Tools[j].Errors
		}
		return report.Tools[i].Runs > report.Tools[j].Runs
	})
	return report, nil
}

// percentile returns the p-th percentile (p in [0,1]) using nearest-rank.
// Input is not required to be sorted; a sorted copy is used internally.
func percentile(xs []int64, p float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := make([]int64, len(xs))
	copy(sorted, xs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// WriteToolStatsReport persists a report to logs/traces/stats-YYYY-MM-DD.json.
// The date in the filename is the report's GeneratedAt date.
func WriteToolStatsReport(dataDir string, r *ToolStatsReport) error {
	dir := filepath.Join(dataDir, "logs", "traces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "stats-"+r.GeneratedAt.Format("2006-01-02")+".json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
