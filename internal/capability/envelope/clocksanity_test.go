package envelope

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCheckBootClock_HappyPath pins normal operation: now is at-or-
// after build time → accepts.
func TestCheckBootClock_HappyPath(t *testing.T) {
	build := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	now := build.Add(48 * time.Hour)
	if err := CheckBootClock(now, build); err != nil {
		t.Errorf("now after build: got %v, want nil", err)
	}
}

// TestCheckBootClock_FutureClockAccepts pins one-sidedness: a wildly
// future clock is fine. We only police the past.
func TestCheckBootClock_FutureClockAccepts(t *testing.T) {
	build := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	now := build.Add(10 * 365 * 24 * time.Hour)
	if err := CheckBootClock(now, build); err != nil {
		t.Errorf("future clock: got %v, want nil", err)
	}
}

// TestCheckBootClock_OneYearBeforeBuildAccepts pins the boundary:
// exactly 1y before build is allowed (strictly less rejects).
func TestCheckBootClock_OneYearBeforeBuildAccepts(t *testing.T) {
	build := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	now := build.Add(-MaxBootSkewBefore)
	if err := CheckBootClock(now, build); err != nil {
		t.Errorf("exactly-1y-before should accept: got %v", err)
	}
}

// TestCheckBootClock_MoreThanOneYearBeforeRefuses pins the §7.7
// guard: a clock more than 1y before build refuses to boot.
func TestCheckBootClock_MoreThanOneYearBeforeRefuses(t *testing.T) {
	build := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	now := build.Add(-MaxBootSkewBefore - time.Hour)
	err := CheckBootClock(now, build)
	if !errors.Is(err, ErrClockTooEarly) {
		t.Errorf("got %v, want ErrClockTooEarly", err)
	}
}

// TestCheckBootClock_DevBuildNoBuildTime pins dev-build behaviour:
// build time zero → no-op (don't refuse a developer's `go run`).
func TestCheckBootClock_DevBuildNoBuildTime(t *testing.T) {
	if err := CheckBootClock(time.Now(), time.Time{}); err != nil {
		t.Errorf("dev build: got %v, want nil", err)
	}
	past := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := CheckBootClock(past, time.Time{}); err != nil {
		t.Errorf("dev build with past clock: got %v, want nil", err)
	}
}

// TestBuildTime_NoInjectionReturnsFalse pins that without ldflags
// injection, BuildTime returns ok=false.
func TestBuildTime_NoInjectionReturnsFalse(t *testing.T) {
	if _, ok := BuildTime(); ok {
		t.Error("BuildTime should be ok=false without ldflags")
	}
}

// TestBuildTime_MalformedReturnsFalse pins parse-failure: a bogus
// injected value yields ok=false rather than panicking.
func TestBuildTime_MalformedReturnsFalse(t *testing.T) {
	prev := buildTime
	buildTime = "not-a-timestamp"
	defer func() { buildTime = prev }()

	if _, ok := BuildTime(); ok {
		t.Error("BuildTime should be ok=false on parse failure")
	}
}

// TestBuildTime_RFC3339Parses pins the happy injection: RFC3339 UTC
// values parse correctly.
func TestBuildTime_RFC3339Parses(t *testing.T) {
	prev := buildTime
	buildTime = "2026-04-26T12:00:00Z"
	defer func() { buildTime = prev }()

	got, ok := BuildTime()
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}
}

// TestWallClockSkew_NoSkewIsNearZero pins quiet operation: wall and
// monotonic advance in lockstep, skew ≈ 0 (within scheduler noise).
func TestWallClockSkew_NoSkewIsNearZero(t *testing.T) {
	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	now := time.Now()
	skew := WallClockSkew(start, now)
	if skew > 100*time.Millisecond || skew < -100*time.Millisecond {
		t.Errorf("idle skew should be near zero, got %s", skew)
	}
}

// TestSkewFromDeltas_ForwardJump pins detection of a forward jump:
// wall advanced 8h, monotonic only 10ms → skew ≈ +8h.
func TestSkewFromDeltas_ForwardJump(t *testing.T) {
	skew := SkewFromDeltas(8*time.Hour, 10*time.Millisecond)
	if skew < 7*time.Hour || skew > 9*time.Hour {
		t.Errorf("expected ~8h skew, got %s", skew)
	}
}

// TestSkewFromDeltas_BackwardJump pins symmetry: wall moved
// backward → negative skew.
func TestSkewFromDeltas_BackwardJump(t *testing.T) {
	skew := SkewFromDeltas(-8*time.Hour, 10*time.Millisecond)
	if skew > -7*time.Hour {
		t.Errorf("backward jump should give skew ≤ -7h, got %s", skew)
	}
}

// TestSkewFromDeltas_NoSkew pins quiet operation: wall and monotonic
// equal → skew zero.
func TestSkewFromDeltas_NoSkew(t *testing.T) {
	if got := SkewFromDeltas(time.Hour, time.Hour); got != 0 {
		t.Errorf("equal deltas: got %s, want 0", got)
	}
}

// TestMonitorClockSkew_RunRespectsContext pins ctx-cancellation: no
// goroutine leak after Done.
func TestMonitorClockSkew_RunRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		MonitorClockSkew(ctx, 10*time.Millisecond, time.Hour, time.Now, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("MonitorClockSkew did not return after ctx cancel")
	}
}
