package controlcenter

import (
	"sync"

	"github.com/alamparelli/alf/internal/comms"
)

// ccAdapter bridges the comms.ChannelAdapter interface to ChatService's
// per-call onEvent callbacks. Since ChatService serializes calls via mu,
// only one callback is active at a time.
type ccAdapter struct {
	mu       sync.Mutex
	callback func(ChatEvent)
}

func newCCAdapter() *ccAdapter {
	return &ccAdapter{}
}

func (a *ccAdapter) Channel() string { return "cc" }

func (a *ccAdapter) SendText(_ comms.ChannelID, _ string) (string, error) {
	return "", nil // CC doesn't send text directly — uses events
}

func (a *ccAdapter) SendReaction(_ comms.ChannelID, _ string, _ string) error {
	return nil // CC reactions are sent via events
}

// OnEvent converts comms.OutEvent to ChatEvent and forwards to the active callback.
// The "done" event is suppressed — the CC wrapper emits its own richer ChatDoneData.
func (a *ccAdapter) OnEvent(_ comms.ChannelID, event comms.OutEvent) {
	a.mu.Lock()
	cb := a.callback
	a.mu.Unlock()
	if cb == nil {
		return
	}

	// Suppress "done" — CC wrapper handles it with richer ChatDoneData.
	if event.Type == "done" {
		return
	}

	cb(outEventToChatEvent(event))
}

func (a *ccAdapter) setCallback(cb func(ChatEvent)) {
	a.mu.Lock()
	a.callback = cb
	a.mu.Unlock()
}

// outEventToChatEvent converts a comms.OutEvent to a CC ChatEvent.
func outEventToChatEvent(event comms.OutEvent) ChatEvent {
	return ChatEvent{
		Type: event.Type,
		Data: event.Data,
	}
}
