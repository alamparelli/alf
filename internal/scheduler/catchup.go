package scheduler

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/robfig/cron/v3"
)

// catchupMaxDowntime caps how far back we look. Longer gaps are treated as a
// fresh start to avoid spamming jobs after extended outages.
const catchupMaxDowntime = 24 * time.Hour

// lastSeenInterval is how often the liveness timestamp is refreshed.
const lastSeenInterval = 60 * time.Second

// lastSeenPath returns the path of the liveness timestamp file.
func lastSeenPath(dataDir string) string {
	return filepath.Join(dataDir, "scheduler", "last_seen")
}

// readLastSeen returns the last persisted liveness timestamp. Zero time if
// the file is missing or unreadable.
func readLastSeen(dataDir string) time.Time {
	data, err := os.ReadFile(lastSeenPath(dataDir))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, string(data))
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeLastSeen persists the current time to the liveness file.
func writeLastSeen(dataDir string) error {
	path := lastSeenPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o644)
}

// startLastSeenWriter starts a goroutine that refreshes the liveness file
// until stopCh is closed.
func startLastSeenWriter(dataDir string, stopCh <-chan struct{}) {
	if err := writeLastSeen(dataDir); err != nil {
		log.Printf("scheduler: last_seen write: %v", err)
	}
	go func() {
		ticker := time.NewTicker(lastSeenInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := writeLastSeen(dataDir); err != nil {
					log.Printf("scheduler: last_seen write: %v", err)
				}
			case <-stopCh:
				return
			}
		}
	}()
}

// catchupDecision describes what runCatchup decided for one job.
type catchupDecision struct {
	JobID  string
	Run    bool   // true = execute once, false = skip
	Reason string // human-readable explanation for logs
}

// planCatchup inspects jobs and decides which ones to catch up. Pure function
// (no side effects) to keep it testable. Callers execute the ones with Run=true.
func planCatchup(jobs []*Job, lastSeen, now time.Time, recurringMinInterval time.Duration) []catchupDecision {
	if lastSeen.IsZero() {
		return nil
	}
	downtime := now.Sub(lastSeen)
	if downtime <= 0 || downtime > catchupMaxDowntime {
		return nil
	}

	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	var out []catchupDecision

	for _, j := range jobs {
		if j == nil || !j.Enabled || j.System {
			// System jobs are re-registered at boot and run on their own
			// cadence; their catch-up adds little value and muddies logs.
			continue
		}

		// One-shot RFC3339.
		if at, err := time.Parse(time.RFC3339, j.Schedule); err == nil {
			if at.After(lastSeen) && !at.After(now) {
				out = append(out, catchupDecision{JobID: j.ID, Run: true,
					Reason: fmt.Sprintf("one-shot missed at %s", at.Format(time.RFC3339))})
			}
			continue
		}

		// Recurring cron — opt-in via min interval.
		if recurringMinInterval <= 0 {
			continue
		}
		sched, err := parser.Parse(j.Schedule)
		if err != nil {
			continue
		}
		first := sched.Next(lastSeen)
		if first.IsZero() || first.After(now) {
			continue // no missed tick
		}
		second := sched.Next(first)
		if second.IsZero() {
			continue
		}
		interval := second.Sub(first)
		if interval < recurringMinInterval {
			continue
		}
		out = append(out, catchupDecision{JobID: j.ID, Run: true,
			Reason: fmt.Sprintf("recurring missed (interval=%s, downtime=%s)", interval, downtime.Round(time.Second))})
	}

	return out
}

// runCatchup executes any jobs that were missed during downtime.
// Runs synchronously so tests can assert ordering, but each job executes
// in its own goroutine to avoid blocking each other.
func (e *Engine) runCatchup(lastSeen time.Time) {
	if lastSeen.IsZero() {
		log.Printf("scheduler: catchup skipped (no last_seen)")
		return
	}
	now := time.Now()
	downtime := now.Sub(lastSeen)
	if downtime > catchupMaxDowntime {
		log.Printf("scheduler: catchup skipped (downtime %s > cap %s)", downtime.Round(time.Second), catchupMaxDowntime)
		return
	}

	decisions := planCatchup(e.store.All(), lastSeen, now, e.cfg.CatchupRecurringMinInterval)
	if len(decisions) == 0 {
		log.Printf("scheduler: catchup: no missed jobs (downtime=%s)", downtime.Round(time.Second))
		return
	}

	for _, d := range decisions {
		j := e.store.Get(d.JobID)
		if j == nil {
			continue
		}
		log.Printf("scheduler: catchup: running %s (%s) — %s", j.ID, j.Name, d.Reason)
		clone := *j
		clone.running = false
		isOneShot := false
		if _, err := time.Parse(time.RFC3339, j.Schedule); err == nil {
			isOneShot = true
		}
		go func(orig *Job, exec *Job, oneShot bool) {
			e.executeJob(exec)
			if oneShot {
				orig.Enabled = false
				e.store.Update(orig)
				if orig.AutoDelete {
					e.store.Remove(orig.ID)
				}
			}
		}(j, &clone, isOneShot)
	}
}
