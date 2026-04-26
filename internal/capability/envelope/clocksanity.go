package envelope

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Clock-sanity checks per ARCHITECTURE-SECURITY.md §7.7. Two
// independent guards:
//
//   1. CheckBootClock — at boot, refuse if the system clock is more
//      than 1 year EARLIER than the build time. A wildly past clock
//      is more likely compromise (or a flat CMOS battery on a
//      compromised box) than NTP drift.
//
//   2. WallClockSkew + MonitorClockSkew — at runtime, sample the
//      wall clock vs the monotonic clock since boot. A delta beyond
//      DefaultSkewThreshold means NTP or a manual operator pushed
//      the wall clock forward; log warn but continue (CRL signed-at
//      checks already absorb minor drift).

// buildTime is injected at link time via:
//
//   go build -ldflags="-X github.com/alamparelli/alf/internal/capability/envelope.buildTime=2026-04-26T12:00:00Z" ...
//
// On a dev build (no ldflags), it stays empty — BuildTime() returns
// ok=false, and CheckBootClock degrades to a no-op (we don't refuse
// to boot a developer's local build just because they didn't set
// ldflags).
var buildTime = ""

// MaxBootSkewBefore is the §7.7 threshold for "wildly past clock":
// 1 year. Anything between (build - 1y) and now is acceptable.
const MaxBootSkewBefore = 365 * 24 * time.Hour

// DefaultSkewThreshold is the §7.7 wall-vs-monotonic warn level: 6h.
const DefaultSkewThreshold = 6 * time.Hour

var (
	// ErrClockTooEarly is returned by CheckBootClock when the system
	// clock predates the build time by more than MaxBootSkewBefore.
	// Daemon refuses to boot.
	ErrClockTooEarly = errors.New("envelope: system clock more than 1y before build time")
)

// BuildTime returns the parsed build-time stamp injected via
// ldflags, or ok=false on a dev build with no injection.
func BuildTime() (time.Time, bool) {
	if buildTime == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, buildTime)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// CheckBootClock refuses if now is more than MaxBootSkewBefore
// earlier than build. Both args are explicit so tests pin behaviour
// without poking globals.
//
// Acceptance is one-sided: now AFTER build is fine (build was made
// in the past, system is running in the future) regardless of how
// far in the future. We only police the past.
func CheckBootClock(now, build time.Time) error {
	if build.IsZero() {
		// Dev build without ldflags. Don't refuse to boot.
		return nil
	}
	threshold := build.Add(-MaxBootSkewBefore)
	if now.Before(threshold) {
		return fmt.Errorf("%w: now=%s build=%s threshold=%s",
			ErrClockTooEarly,
			now.Format(time.RFC3339),
			build.Format(time.RFC3339),
			threshold.Format(time.RFC3339))
	}
	return nil
}

// WallClockSkew computes the divergence between the wall-clock delta
// and the monotonic delta from start to now. BOTH arguments must
// come from time.Now() (Go's time.Time carries monotonic by
// default). Stripping monotonic from `now` (e.g. via .Round(0)) makes
// time.Sub fall back to wall arithmetic on both sides — the result
// is then 0 by construction. Tests must use real time.Now() values.
//
// Returns positive if the wall jumped FORWARD relative to monotonic
// (NTP push, manual set forward); negative if it jumped backward.
// Caller compares abs() against a threshold.
func WallClockSkew(start, now time.Time) time.Duration {
	mono := now.Sub(start)                   // monotonic when both have it
	wall := now.Round(0).Sub(start.Round(0)) // wall-only delta
	return SkewFromDeltas(wall, mono)
}

// SkewFromDeltas is the pure math part of WallClockSkew. Exposed for
// tests that synthesize wall vs monotonic divergence — Go's time
// package gives no way to construct a time.Time with mismatched
// wall/monotonic, so synthetic jump scenarios pass deltas directly.
func SkewFromDeltas(wall, mono time.Duration) time.Duration {
	return wall - mono
}

// MonitorClockSkew samples the wall-vs-monotonic divergence on every
// `sample` tick and calls logf when the absolute skew first crosses
// `threshold` (and again on every subsequent crossing — operators
// see repeated warnings rather than one buried in startup noise).
//
// Runs until ctx is done. Stateless across restarts: starts fresh
// each call. Test-friendly because nowFn is injectable.
func MonitorClockSkew(ctx context.Context, sample, threshold time.Duration, nowFn func() time.Time, logf func(string, ...any)) {
	if nowFn == nil {
		nowFn = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if threshold <= 0 {
		threshold = DefaultSkewThreshold
	}
	if sample <= 0 {
		sample = time.Hour
	}

	start := nowFn()
	ticker := time.NewTicker(sample)
	defer ticker.Stop()

	var lastWarned bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := nowFn()
			skew := WallClockSkew(start, now)
			abs := skew
			if abs < 0 {
				abs = -abs
			}
			if abs > threshold {
				if !lastWarned {
					logf("[clock] WALL CLOCK SKEW %s (threshold %s) — NTP/operator changed wall clock since boot", skew.Round(time.Minute), threshold)
					lastWarned = true
				}
			} else if lastWarned {
				logf("[clock] wall clock skew back below threshold (%s)", skew.Round(time.Minute))
				lastWarned = false
			}
		}
	}
}
