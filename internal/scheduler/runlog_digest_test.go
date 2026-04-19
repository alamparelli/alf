package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestFormattedDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{5, "5ms"},
		{999, "999ms"},
		{1500, "1.5s"},
		{59_500, "59.5s"},
		{125_000, "2.1m"},
	}
	for _, tt := range tests {
		r := RunRecord{DurationMs: tt.ms}
		if got := r.FormattedDuration(); got != tt.want {
			t.Errorf("FormattedDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestDailyDigest_Empty(t *testing.T) {
	rl := NewRunLog(t.TempDir())
	if got := rl.DailyDigest(time.Now().Add(-24 * time.Hour)); got != "" {
		t.Errorf("expected empty digest when no records, got %q", got)
	}
}

func TestDailyDigest_AllOK(t *testing.T) {
	rl := NewRunLog(t.TempDir())
	now := time.Now()
	for i := 0; i < 3; i++ {
		rl.Append(RunRecord{
			JobID:      "job-a",
			JobName:    "Alpha",
			StartedAt:  now.Add(time.Duration(i) * time.Minute),
			DurationMs: 500,
			Status:     "ok",
			CostUSD:    0.001,
		})
	}

	digest := rl.DailyDigest(now.Add(-time.Hour))
	if !strings.Contains(digest, "Alpha") {
		t.Errorf("digest missing job name: %s", digest)
	}
	if !strings.Contains(digest, "All 3 runs succeeded") {
		t.Errorf("digest missing all-ok summary: %s", digest)
	}
}

func TestDailyDigest_MixedStatuses(t *testing.T) {
	rl := NewRunLog(t.TempDir())
	now := time.Now()
	rl.Append(RunRecord{JobID: "j", JobName: "Job", StartedAt: now, Status: "ok", DurationMs: 2000})
	rl.Append(RunRecord{JobID: "j", JobName: "Job", StartedAt: now.Add(time.Minute), Status: "error", Error: "boom", DurationMs: 100})
	rl.Append(RunRecord{JobID: "j", JobName: "Job", StartedAt: now.Add(2 * time.Minute), Status: "skipped"})

	digest := rl.DailyDigest(now.Add(-time.Hour))
	if !strings.Contains(digest, "FAIL 1/3") {
		t.Errorf("expected FAIL 1/3 summary, got %s", digest)
	}
	if !strings.Contains(digest, "err: boom") {
		t.Errorf("expected error line, got %s", digest)
	}
	if !strings.Contains(digest, "1 OK, 1 failed") {
		t.Errorf("expected totals, got %s", digest)
	}
}

func TestDailyDigest_TruncatesLongErrors(t *testing.T) {
	rl := NewRunLog(t.TempDir())
	now := time.Now()
	longErr := strings.Repeat("x", 200)
	rl.Append(RunRecord{JobID: "j", JobName: "Job", StartedAt: now, Status: "error", Error: longErr})

	digest := rl.DailyDigest(now.Add(-time.Hour))
	if !strings.Contains(digest, "...") {
		t.Errorf("expected long error to be truncated with ellipsis, got %s", digest)
	}
}

func TestTruncate_IsNoOp(t *testing.T) {
	rl := NewRunLog(t.TempDir())
	// Truncate is a no-op kept for API compatibility; must not panic.
	rl.Truncate("any", 10)
}

func TestAppendAndTruncate_AppendsAndTracksPurge(t *testing.T) {
	rl := NewRunLog(t.TempDir())
	now := time.Now()
	// First call: lastPurge is zero, so purge runs; lastPurge is stamped.
	rl.appendAndTruncate(RunRecord{JobID: "j", JobName: "J", StartedAt: now, Status: "ok"})
	if rl.lastPurge.IsZero() {
		t.Error("expected lastPurge to be stamped after first appendAndTruncate call")
	}
	// Second call: within 24h, purge should NOT re-run — lastPurge should not change.
	firstPurge := rl.lastPurge
	rl.appendAndTruncate(RunRecord{JobID: "j", JobName: "J", StartedAt: now.Add(time.Minute), Status: "ok"})
	if !rl.lastPurge.Equal(firstPurge) {
		t.Error("lastPurge must not re-stamp within 24h window")
	}
	// Both records must be persisted.
	if recs := rl.Recent("j", 10); len(recs) != 2 {
		t.Errorf("expected 2 records, got %d", len(recs))
	}
}
