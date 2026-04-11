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
	now := time.Now()
	for i := 0; i < 3; i++ {
		rl.Append(RunRecord{
			JobID:      "test-job",
			JobName:    "Test Job",
			Tier:       "direct",
			StartedAt:  now.Add(time.Duration(i) * time.Minute),
			DurationMs: int64(100 + i*50),
			Status:     "ok",
			OutputLen:  42,
		})
	}

	// Should write to a single daily file.
	dailyFile := filepath.Join(dir, now.Format("2006-01-02")+".jsonl")
	if _, err := os.Stat(dailyFile); err != nil {
		t.Fatalf("expected daily file %s to exist", dailyFile)
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

func TestRunLogMultipleJobsSameDay(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	now := time.Now()
	rl.Append(RunRecord{JobID: "job-a", StartedAt: now, Status: "ok", DurationMs: 100})
	rl.Append(RunRecord{JobID: "job-b", StartedAt: now.Add(time.Minute), Status: "error", DurationMs: 200})
	rl.Append(RunRecord{JobID: "job-a", StartedAt: now.Add(2 * time.Minute), Status: "ok", DurationMs: 300})

	// All in one file.
	entries, _ := os.ReadDir(dir)
	jsonlCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jsonl" {
			jsonlCount++
		}
	}
	if jsonlCount != 1 {
		t.Fatalf("expected 1 daily file, got %d", jsonlCount)
	}

	// Recent filters by job.
	recsA := rl.Recent("job-a", 0)
	if len(recsA) != 2 {
		t.Fatalf("expected 2 records for job-a, got %d", len(recsA))
	}
	recsB := rl.Recent("job-b", 0)
	if len(recsB) != 1 {
		t.Fatalf("expected 1 record for job-b, got %d", len(recsB))
	}
}

func TestRunLogStats(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	now := time.Now()
	rl.Append(RunRecord{JobID: "j1", StartedAt: now, DurationMs: 100, Status: "ok"})
	rl.Append(RunRecord{JobID: "j1", StartedAt: now.Add(time.Minute), DurationMs: 200, Status: "ok"})
	rl.Append(RunRecord{JobID: "j1", StartedAt: now.Add(2 * time.Minute), DurationMs: 300, Status: "error", Error: "boom"})

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

func TestRunLogRecentAll(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	now := time.Now()
	rl.Append(RunRecord{JobID: "a", StartedAt: now.Add(-2 * time.Minute), Status: "ok"})
	rl.Append(RunRecord{JobID: "b", StartedAt: now.Add(-1 * time.Minute), Status: "ok"})
	rl.Append(RunRecord{JobID: "a", StartedAt: now, Status: "error"})

	all := rl.RecentAll(10)
	if len(all) != 3 {
		t.Fatalf("expected 3 records, got %d", len(all))
	}
	// Newest first.
	if all[0].JobID != "a" || all[0].Status != "error" {
		t.Errorf("expected newest record first (job a, error), got job=%s status=%s", all[0].JobID, all[0].Status)
	}
}

func TestRunLogPurgeOld(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	// Create a "today" file.
	today := time.Now()
	rl.Append(RunRecord{JobID: "j1", StartedAt: today, Status: "ok"})

	// Create an "old" file (100 days ago).
	oldDate := today.AddDate(0, 0, -100)
	oldFile := filepath.Join(dir, oldDate.Format("2006-01-02")+".jsonl")
	os.WriteFile(oldFile, []byte(`{"job_id":"old","status":"ok"}`+"\n"), 0o644)

	// Create a "recent" file (30 days ago).
	recentDate := today.AddDate(0, 0, -30)
	recentFile := filepath.Join(dir, recentDate.Format("2006-01-02")+".jsonl")
	os.WriteFile(recentFile, []byte(`{"job_id":"recent","status":"ok"}`+"\n"), 0o644)

	// Create a legacy per-job file (non-date name).
	legacyFile := filepath.Join(dir, "some-job-id.jsonl")
	os.WriteFile(legacyFile, []byte(`{"job_id":"legacy","status":"ok"}`+"\n"), 0o644)

	rl.PurgeOld()

	// Old file should be gone.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("expected old file to be purged")
	}
	// Legacy file should be gone.
	if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
		t.Error("expected legacy per-job file to be purged")
	}
	// Recent file should remain.
	if _, err := os.Stat(recentFile); err != nil {
		t.Error("expected recent file to still exist")
	}
	// Today's file should remain.
	todayFile := filepath.Join(dir, today.Format("2006-01-02")+".jsonl")
	if _, err := os.Stat(todayFile); err != nil {
		t.Error("expected today file to still exist")
	}
}

func TestRunLogCleanupCallsPurge(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	// Legacy per-job file.
	legacyFile := filepath.Join(dir, "deleted.jsonl")
	os.WriteFile(legacyFile, []byte(`{"job_id":"deleted","status":"ok"}`+"\n"), 0o644)

	// Cleanup still works (delegates to PurgeOld).
	rl.Cleanup(map[string]bool{"active": true})

	if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
		t.Error("expected legacy file to be removed by Cleanup")
	}
}

func TestRunLogLastRunFor(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	// Nothing yet.
	if rec := rl.LastRunFor("missing"); rec != nil {
		t.Errorf("expected nil for unknown job, got %+v", rec)
	}

	// Write records across two days for the same job; newest should win.
	yesterday := time.Now().AddDate(0, 0, -1).Truncate(24 * time.Hour).Add(10 * time.Hour)
	today := time.Now().Truncate(24 * time.Hour).Add(10 * time.Hour)

	rl.Append(RunRecord{JobID: "mem-consolidate", StartedAt: yesterday, Status: "ok"})
	rl.Append(RunRecord{JobID: "other", StartedAt: today.Add(time.Minute), Status: "ok"})
	rl.Append(RunRecord{JobID: "mem-consolidate", StartedAt: today, Status: "error", Error: "boom"})

	rec := rl.LastRunFor("mem-consolidate")
	if rec == nil {
		t.Fatal("expected a record, got nil")
	}
	if !rec.StartedAt.Equal(today) {
		t.Errorf("expected newest record (today), got %v", rec.StartedAt)
	}
	if rec.Status != "error" || rec.Error != "boom" {
		t.Errorf("expected error status with boom, got %q/%q", rec.Status, rec.Error)
	}

	// Different job falls through to its own most recent record.
	rec = rl.LastRunFor("other")
	if rec == nil || rec.JobID != "other" {
		t.Fatalf("expected 'other' record, got %+v", rec)
	}
}

func TestRunLogSince(t *testing.T) {
	dir := t.TempDir()
	rl := NewRunLog(dir)

	now := time.Now()
	rl.Append(RunRecord{JobID: "j1", StartedAt: now.Add(-2 * time.Hour), Status: "ok"})
	rl.Append(RunRecord{JobID: "j1", StartedAt: now.Add(-30 * time.Minute), Status: "error"})
	rl.Append(RunRecord{JobID: "j2", StartedAt: now.Add(-10 * time.Minute), Status: "ok"})

	recs := rl.Since(now.Add(-1 * time.Hour))
	if len(recs) != 2 {
		t.Fatalf("expected 2 records since 1h ago, got %d", len(recs))
	}
}
