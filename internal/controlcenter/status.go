package controlcenter

import (
	"fmt"
	"strings"
	"time"
)

// UpdateChecker provides the latest available version (if any).
type UpdateChecker interface {
	LatestVersion() string
}

// daemonStatusProvider implements StatusProvider using shared Stats.
type daemonStatusProvider struct {
	stats     *Stats
	version   string
	chatStore *ChatStore
	updater   UpdateChecker // may be nil
}

// NewStatusProvider creates a StatusProvider from shared stats.
func NewStatusProvider(stats *Stats, version string, chatStore *ChatStore) *daemonStatusProvider {
	return &daemonStatusProvider{stats: stats, version: version, chatStore: chatStore}
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
	if p.chatStore != nil {
		ds.Session = p.currentSession()
	}

	return ds
}

// currentSession scans recent messages to find the latest session and compute stats.
func (p *daemonStatusProvider) currentSession() *SessionStatus {
	msgs := p.chatStore.Recent(0) // all in ring buffer
	if len(msgs) == 0 {
		return nil
	}

	// Find the latest interactive session ID (skip scheduled job sessions).
	var sessionID string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].SessionID != "" && !strings.HasPrefix(msgs[i].SessionID, "scheduled:") {
			sessionID = msgs[i].SessionID
			break
		}
	}
	if sessionID == "" {
		return nil
	}

	// Count messages and sum cost for this session.
	var count int
	var cost float64
	for _, m := range msgs {
		if m.SessionID == sessionID {
			count++
			cost += m.CostUSD
		}
	}

	// Shorten session ID for display.
	displayID := sessionID
	if len(displayID) > 12 {
		displayID = displayID[:12]
	}

	return &SessionStatus{
		ID:           displayID,
		MessageCount: count,
		CostUSD:      cost,
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
