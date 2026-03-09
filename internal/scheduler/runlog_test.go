package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunLogAppendAndRecent(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	// Append 3 records.
	for i := 0; i < 3; i++ {
		rl.Append(RunRecord{
			JobID:      "test-job",
			JobName:    "Test Job",
			Tier:       "direct",
			StartedAt:  time.Now().Add(time.Duration(i) * time.Minute),
			DurationMs: int64(100 + i*50),
			Status:     "ok",
			OutputLen:  42,
		})
	}

	// Recent should return newest first.
	recs := rl.Recent("test-job", 10)
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	if recs[0].DurationMs != 200 {
		t.Errorf("expected newest record first (200ms), got %d", recs[0].DurationMs)
	}

	// Limit works.
	recs = rl.Recent("test-job", 2)
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
}

func TestRunLogStats(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	// 2 ok, 1 error.
	rl.Append(RunRecord{JobID: "j1", StartedAt: time.Now(), DurationMs: 100, Status: "ok"})
	rl.Append(RunRecord{JobID: "j1", StartedAt: time.Now(), DurationMs: 200, Status: "ok"})
	rl.Append(RunRecord{JobID: "j1", StartedAt: time.Now(), DurationMs: 300, Status: "error", Error: "boom"})

	stats := rl.Stats("j1")
	if stats == nil {
		t.Fatal("expected stats, got nil")
	}
	if stats.TotalRuns != 3 {
		t.Errorf("expected 3 runs, got %d", stats.TotalRuns)
	}
	if stats.OkCount != 2 {
		t.Errorf("expected 2 ok, got %d", stats.OkCount)
	}
	if stats.FailCount != 1 {
		t.Errorf("expected 1 fail, got %d", stats.FailCount)
	}
	if stats.LastStatus != "error" {
		t.Errorf("expected last status 'error', got %q", stats.LastStatus)
	}
	if stats.Streak != 1 {
		t.Errorf("expected streak 1, got %d", stats.Streak)
	}
	if stats.AvgDurationMs != 200 {
		t.Errorf("expected avg 200ms, got %d", stats.AvgDurationMs)
	}
}

func TestRunLogTruncate(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	for i := 0; i < 10; i++ {
		rl.Append(RunRecord{
			JobID:      "j2",
			StartedAt:  time.Now().Add(time.Duration(i) * time.Minute),
			DurationMs: int64(i * 10),
			Status:     "ok",
		})
	}

	rl.Truncate("j2", 5)

	recs := rl.Recent("j2", 0)
	if len(recs) != 5 {
		t.Fatalf("expected 5 records after truncate, got %d", len(recs))
	}
	// Should keep the 5 newest.
	if recs[0].DurationMs != 90 {
		t.Errorf("expected newest record (90ms), got %d", recs[0].DurationMs)
	}
}

func TestRunLogRecentAll(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	rl.Append(RunRecord{JobID: "a", StartedAt: time.Now().Add(-2 * time.Minute), Status: "ok"})
	rl.Append(RunRecord{JobID: "b", StartedAt: time.Now().Add(-1 * time.Minute), Status: "ok"})
	rl.Append(RunRecord{JobID: "a", StartedAt: time.Now(), Status: "error"})

	all := rl.RecentAll(10)
	if len(all) != 3 {
		t.Fatalf("expected 3 records, got %d", len(all))
	}
	// Newest first.
	if all[0].JobID != "a" || all[0].Status != "error" {
		t.Errorf("expected newest record first (job a, error), got job=%s status=%s", all[0].JobID, all[0].Status)
	}
}

func TestRunLogCleanup(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	rl.Append(RunRecord{JobID: "active", Status: "ok", StartedAt: time.Now()})
	rl.Append(RunRecord{JobID: "deleted", Status: "ok", StartedAt: time.Now()})

	// Only "active" is still a real job.
	rl.Cleanup(map[string]bool{"active": true})

	// "deleted" log should be gone.
	if _, err := os.Stat(filepath.Join(dir, "deleted.jsonl")); !os.IsNotExist(err) {
		t.Error("expected deleted.jsonl to be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "active.jsonl")); err != nil {
		t.Error("expected active.jsonl to still exist")
	}
}
