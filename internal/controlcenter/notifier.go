package controlcenter

import "log"

// channelNotifier implements Notifier using a buffered channel.
type channelNotifier struct {
	ch chan ReloadEvent
}

// NewChannelNotifier creates a Notifier that sends to the given channel.
// The channel should be buffered to avoid blocking.
func NewChannelNotifier(ch chan ReloadEvent) Notifier {
	return &channelNotifier{ch: ch}
}

// notifyReload fires zero or more reload events on n.
// Safe to call with a nil Notifier (no-op).
func notifyReload(n Notifier, events ...ReloadEvent) {
	if n == nil {
		return
	}
	for _, e := range events {
		n.Notify(e)
	}
}

// Notify sends a reload event. Non-blocking: drops if channel is full.
func (n *channelNotifier) Notify(event ReloadEvent) {
	select {
	case n.ch <- event:
	default:
		log.Printf("WARNING: reload event %d dropped (channel full)", event)
	}
}
