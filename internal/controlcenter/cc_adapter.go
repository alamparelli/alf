package controlcenter

import (
	"context"
	"fmt"
	"sync"

	"github.com/alamparelli/alf/internal/comms"
	"github.com/alamparelli/alf/internal/memory"
)

// ccAdapter bridges the comms.ChannelAdapter interface to ChatService's
// per-call onEvent callbacks. Since ChatService serializes calls via mu,
// only one callback is active at a time.
type ccAdapter struct {
	mu       sync.Mutex
	callback func(ChatEvent)

	// Optional deps for standalone message injection (notifications, chain results).
	Memory      memory.Store
	EventBroker *EventBroker
}

func newCCAdapter() *ccAdapter {
	return &ccAdapter{}
}

func (a *ccAdapter) Channel() string { return "cc" }

// SendText injects a standalone message into the CC chat (used for async notifications).
func (a *ccAdapter) SendText(_ comms.ChannelID, text string) (string, error) {
	if a.Memory == nil {
		return "", nil
	}
	ctx := context.Background()
	convID, _ := a.Memory.LatestConvID(ctx, "cc")
	if convID == "" {
		convID = "_system"
		_ = a.Memory.EnsureConv(ctx, convID, "", "cc")
	}
	msg := memory.Message{
		Role:    "assistant",
		Channel: "cc",
		Content: text,
		Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: text}},
	}
	stored, err := a.Memory.AppendMessage(ctx, convID, msg)
	if err != nil {
		return "", err
	}
	if a.EventBroker != nil {
		a.EventBroker.EmitWithData(EventNewMessage, fmt.Sprintf(`{"conv_id":%q}`, string(convID)))
	}
	return string(stored.ID), nil
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
