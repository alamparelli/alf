package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastSeenRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if !readLastSeen(dir).IsZero() {
		t.Fatal("expected zero time when file absent")
	}
	if err := writeLastSeen(dir); err != nil {
		t.Fatalf("writeLastSeen: %v", err)
	}
	got := readLastSeen(dir)
	if got.IsZero() {
		t.Fatal("expected non-zero after write")
	}
	if time.Since(got) > time.Second {
		t.Fatalf("timestamp too old: %v", got)
	}
}

func TestLastSeen_CapsAtMaxLines(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < lastSeenMaxLines+5; i++ {
		if err := writeLastSeen(dir); err != nil {
			t.Fatalf("write #%d: %v", i, err)
		}
	}
	data, err := os.ReadFile(lastSeenPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var nonEmpty int
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != lastSeenMaxLines {
		t.Fatalf("expected %d lines, got %d", lastSeenMaxLines, nonEmpty)
	}
	if readLastSeen(dir).IsZero() {
		t.Fatal("expected readable timestamp after rotation")
	}
}

func TestReadLastSeen_IgnoresMalformedTrailingLines(t *testing.T) {
	dir := t.TempDir()
	path := lastSeenPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	good := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	content := good + "\nnot-a-time\n\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readLastSeen(dir)
	if got.IsZero() {
		t.Fatal("expected fallback to last valid line")
	}
}

func TestReadLastSeen_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := lastSeenPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("not-a-time"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !readLastSeen(dir).IsZero() {
		t.Fatal("expected zero for malformed content")
	}
}

func TestPlanCatchup_NoLastSeen(t *testing.T) {
	now := time.Now()
	jobs := []*Job{{ID: "a", Enabled: true, Schedule: now.Add(-time.Hour).Format(time.RFC3339)}}
	got := planCatchup(jobs, time.Time{}, now, 0)
	if len(got) != 0 {
		t.Fatalf("expected no decisions, got %d", len(got))
	}
}

func TestPlanCatchup_ExceedsCap(t *testing.T) {
	now := time.Now()
	lastSeen := now.Add(-25 * time.Hour)
	jobs := []*Job{{ID: "a", Enabled: true, Schedule: now.Add(-time.Hour).Format(time.RFC3339)}}
	if got := planCatchup(jobs, lastSeen, now, 0); len(got) != 0 {
		t.Fatalf("expected catchup skipped past cap, got %d", len(got))
	}
}

func TestPlanCatchup_OneShot_Missed(t *testing.T) {
	now := time.Now()
	lastSeen := now.Add(-2 * time.Hour)
	missedAt := now.Add(-30 * time.Minute)
	jobs := []*Job{
		{ID: "run-me", Enabled: true, Schedule: missedAt.Format(time.RFC3339)},
		{ID: "too-old", Enabled: true, Schedule: lastSeen.Add(-time.Minute).Format(time.RFC3339)},
		{ID: "future", Enabled: true, Schedule: now.Add(time.Hour).Format(time.RFC3339)},
		{ID: "disabled", Enabled: false, Schedule: missedAt.Format(time.RFC3339)},
	}
	got := planCatchup(jobs, lastSeen, now, 0)
	if len(got) != 1 || got[0].JobID != "run-me" {
		t.Fatalf("expected only run-me, got %+v", got)
	}
}

func TestPlanCatchup_Recurring_RespectsThreshold(t *testing.T) {
	now := time.Now()
	lastSeen := now.Add(-12 * time.Hour)

	// @every 5m — interval below any reasonable threshold.
	fast := &Job{ID: "fast", Enabled: true, Schedule: "@every 5m"}
	// @every 6h — meets threshold.
	slow := &Job{ID: "slow", Enabled: true, Schedule: "@every 6h"}

	// threshold disabled → neither runs.
	if got := planCatchup([]*Job{fast, slow}, lastSeen, now, 0); len(got) != 0 {
		t.Fatalf("disabled threshold should skip all, got %+v", got)
	}

	// threshold 6h → only slow runs.
	got := planCatchup([]*Job{fast, slow}, lastSeen, now, 6*time.Hour)
	if len(got) != 1 || got[0].JobID != "slow" {
		t.Fatalf("expected only slow, got %+v", got)
	}
}

func TestPlanCatchup_Recurring_NoMissedTick(t *testing.T) {
	now := time.Now()
	lastSeen := now.Add(-10 * time.Minute) // under 6h interval → no tick missed
	slow := &Job{ID: "slow", Enabled: true, Schedule: "@every 6h"}
	if got := planCatchup([]*Job{slow}, lastSeen, now, 6*time.Hour); len(got) != 0 {
		t.Fatalf("expected no catchup when no tick was missed, got %+v", got)
	}
}

func TestPlanCatchup_SkipsSystemJobs(t *testing.T) {
	now := time.Now()
	lastSeen := now.Add(-2 * time.Hour)
	jobs := []*Job{
		{ID: "sys", System: true, Enabled: true, Schedule: "@every 6h"},
	}
	if got := planCatchup(jobs, lastSeen, now, 6*time.Hour); len(got) != 0 {
		t.Fatalf("system jobs must be skipped, got %+v", got)
	}
}
