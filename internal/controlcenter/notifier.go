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

// Notify sends a reload event. Non-blocking: drops if channel is full.
func (n *channelNotifier) Notify(event ReloadEvent) {
	select {
	case n.ch <- event:
	default:
		log.Printf("WARNING: reload event %d dropped (channel full)", event)
	}
}
