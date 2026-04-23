package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSONL(t *testing.T, path string, tracers []Tracer) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, tr := range tracers {
		if err := enc.Encode(tr); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAggregateToolStats_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	r, err := AggregateToolStats(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalRuns != 0 || len(r.Tools) != 0 {
		t.Fatalf("expected empty report, got %+v", r)
	}
}

func TestAggregateToolStats_CountsAndSorts(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "traces", today+".jsonl")

	mk := func(tool string, dur int64, isErr bool, errMsg string) Span {
		tags := map[string]string{"tool": tool, "exit_code": "0"}
		if isErr {
			tags["is_error"] = "true"
			tags["error"] = errMsg
			tags["exit_code"] = "1"
		}
		return Span{Name: "tool_exec", DurationMs: dur, Tags: tags}
	}

	tracers := []Tracer{
		{Spans: []Span{
			mk("bash", 100, false, ""),
			mk("bash", 200, true, "boom"),
			mk("grep", 50, false, ""),
		}},
		{Spans: []Span{
			mk("bash", 300, true, "kaboom"),
			mk("grep", 40, false, ""),
		}},
	}
	writeJSONL(t, path, tracers)

	r, err := AggregateToolStats(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalRuns != 5 || r.TotalErrors != 2 {
		t.Fatalf("totals wrong: runs=%d errors=%d", r.TotalRuns, r.TotalErrors)
	}
	if r.Tools[0].Tool != "bash" {
		t.Fatalf("expected bash first (most errors), got %s", r.Tools[0].Tool)
	}
	bash := r.Tools[0]
	if bash.Runs != 3 || bash.Errors != 2 {
		t.Fatalf("bash stats: %+v", bash)
	}
	if bash.ErrRate < 0.66 || bash.ErrRate > 0.67 {
		t.Fatalf("expected err_rate ~0.666, got %v", bash.ErrRate)
	}
	if bash.MaxMs != 300 {
		t.Fatalf("expected max 300, got %d", bash.MaxMs)
	}
	if bash.LastErr == "" {
		t.Fatal("expected last_error populated")
	}
}

func TestAggregateToolStats_SkipsNonToolExec(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "traces", today+".jsonl")
	tracers := []Tracer{
		{Spans: []Span{
			{Name: "llm_call", DurationMs: 500, Tags: map[string]string{"tool": "bash"}},
			{Name: "tool_exec", DurationMs: 10, Tags: map[string]string{"tool": "grep"}},
		}},
	}
	writeJSONL(t, path, tracers)
	r, _ := AggregateToolStats(dir, 1)
	if r.TotalRuns != 1 || r.Tools[0].Tool != "grep" {
		t.Fatalf("expected only grep counted, got %+v", r.Tools)
	}
}

func TestAggregateToolStats_MalformedLineSkipped(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "traces", today+".jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	content := `{"spans":[{"name":"tool_exec","duration_ms":1,"tags":{"tool":"ok"}}]}` + "\n" +
		`not-json` + "\n" +
		`{"spans":[{"name":"tool_exec","duration_ms":2,"tags":{"tool":"ok"}}]}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := AggregateToolStats(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalRuns != 2 {
		t.Fatalf("expected malformed line skipped, got runs=%d", r.TotalRuns)
	}
}

func TestWriteToolStatsReport(t *testing.T) {
	dir := t.TempDir()
	r := &ToolStatsReport{GeneratedAt: time.Now(), WindowDays: 7}
	if err := WriteToolStatsReport(dir, r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "logs", "traces", "stats-"+r.GeneratedAt.Format("2006-01-02")+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected report at %s: %v", path, err)
	}
}

func TestPercentile(t *testing.T) {
	if got := percentile(nil, 0.95); got != 0 {
		t.Fatalf("empty: %d", got)
	}
	xs := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentile(xs, 0.95); got != 100 {
		t.Fatalf("p95 expected 100, got %d", got)
	}
	if got := percentile(xs, 0.50); got != 50 {
		t.Fatalf("p50 expected 50, got %d", got)
	}
}
