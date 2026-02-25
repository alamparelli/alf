package controlcenter

import (
	"fmt"
	"time"
)

// daemonStatusProvider implements StatusProvider using shared Stats.
type daemonStatusProvider struct {
	stats   *Stats
	version string
}

// NewStatusProvider creates a StatusProvider from shared stats.
func NewStatusProvider(stats *Stats, version string) StatusProvider {
	return &daemonStatusProvider{stats: stats, version: version}
}

func (p *daemonStatusProvider) Status() DaemonStatus {
	uptime := time.Since(p.stats.StartedAt)

	var lastMsg *string
	if t := p.stats.LastMessage.Load(); t != nil {
		s := t.Format(time.RFC3339)
		lastMsg = &s
	}

	return DaemonStatus{
		Status:       "running",
		Uptime:       formatDuration(uptime),
		MessageCount: p.stats.MessageCount.Load(),
		LastMessage:  lastMsg,
		Version:      p.version,
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
