package controlcenter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/memory"
)

// UpdateChecker provides the latest available version (if any).
type UpdateChecker interface {
	LatestVersion() string
}

// daemonStatusProvider implements StatusProvider using shared Stats.
type daemonStatusProvider struct {
	stats   *Stats
	version string
	mem     memory.Store
	updater UpdateChecker // may be nil
}

// NewStatusProvider creates a StatusProvider from shared stats.
func NewStatusProvider(stats *Stats, version string, mem memory.Store) *daemonStatusProvider {
	return &daemonStatusProvider{stats: stats, version: version, mem: mem}
}

// SetUpdater attaches the update checker for version status reporting.
func (p *daemonStatusProvider) SetUpdater(u UpdateChecker) {
	p.updater = u
}

func (p *daemonStatusProvider) Status() DaemonStatus {
	uptime := time.Since(p.stats.StartedAt)

	var lastMsg *string
	if t := p.stats.LastMessage.Load(); t != nil {
		s := t.Format(time.RFC3339)
		lastMsg = &s
	}

	ds := DaemonStatus{
		Status:       "running",
		Uptime:       formatDuration(uptime),
		MessageCount: p.stats.MessageCount.Load(),
		LastMessage:  lastMsg,
		Version:      p.version,
	}

	if p.updater != nil {
		if latest := p.updater.LatestVersion(); latest != "" {
			ds.UpdateAvailable = latest
		}
	}

	// Compute current session stats from recent messages.
	if p.mem != nil {
		ds.Session = p.currentSession()
	}

	return ds
}

// currentSession queries the memory store for the latest interactive session.
//
// TODO(#336): chatdb.SessionStats used to do this in a single indexed SQL
// query. memory.Store has no equivalent, so we walk convs + messages in
// Go and pick the latest non-scheduled session. Accept the O(n) cost for
// now — session stats is a polled status endpoint, not a hot path.
func (p *daemonStatusProvider) currentSession() *SessionStatus {
	ctx := context.Background()
	convs, err := p.mem.ListConvs(ctx, memory.ConvFilter{IncludeArchived: true})
	if err != nil {
		return nil
	}

	var latestSessionID string
	var latestCreated int64
	type sessStat struct {
		count int
		cost  float64
	}
	stats := make(map[string]*sessStat)

	for _, c := range convs {
		msgs, err := p.mem.ListMessages(ctx, c.ID, memory.ListOpts{})
		if err != nil {
			continue
		}
		for _, m := range msgs {
			sid := m.SessionID
			if sid == "" || strings.HasPrefix(sid, "scheduled:") {
				continue
			}
			s, ok := stats[sid]
			if !ok {
				s = &sessStat{}
				stats[sid] = s
			}
			s.count++
			s.cost += m.CostUSD
			if m.CreatedAt > latestCreated {
				latestCreated = m.CreatedAt
				latestSessionID = sid
			}
		}
	}
	if latestSessionID == "" {
		return nil
	}

	displayID := latestSessionID
	if len(displayID) > 12 {
		displayID = displayID[:12]
	}

	return &SessionStatus{
		ID:           displayID,
		MessageCount: stats[latestSessionID].count,
		CostUSD:      stats[latestSessionID].cost,
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
