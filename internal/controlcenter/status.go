package controlcenter

import (
	"fmt"
	"time"

	"github.com/alamparelli/alf/internal/chatdb"
)

// UpdateChecker provides the latest available version (if any).
type UpdateChecker interface {
	LatestVersion() string
}

// daemonStatusProvider implements StatusProvider using shared Stats.
type daemonStatusProvider struct {
	stats   *Stats
	version string
	chatDB  *chatdb.DB
	updater UpdateChecker // may be nil
}

// NewStatusProvider creates a StatusProvider from shared stats.
func NewStatusProvider(stats *Stats, version string, chatDB *chatdb.DB) *daemonStatusProvider {
	return &daemonStatusProvider{stats: stats, version: version, chatDB: chatDB}
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
	if p.chatDB != nil {
		ds.Session = p.currentSession()
	}

	return ds
}

// currentSession queries ChatDB for the latest interactive session stats.
func (p *daemonStatusProvider) currentSession() *SessionStatus {
	sessionID, count, cost, err := p.chatDB.SessionStats("scheduled:")
	if err != nil || sessionID == "" {
		return nil
	}

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
