package controlcenter

import (
	"fmt"
	"sync"

	"github.com/alamparelli/alf/internal/chatdb"
	"github.com/alamparelli/alf/internal/comms"
)

// ccAdapter bridges the comms.ChannelAdapter interface to ChatService's
// per-call onEvent callbacks. Since ChatService serializes calls via mu,
// only one callback is active at a time.
type ccAdapter struct {
	mu       sync.Mutex
	callback func(ChatEvent)

	// Optional deps for standalone message injection (notifications, chain results).
	ChatDB      *chatdb.DB
	EventBroker *EventBroker
}

func newCCAdapter() *ccAdapter {
	return &ccAdapter{}
}

func (a *ccAdapter) Channel() string { return "cc" }

// SendText injects a standalone message into the CC chat (used for async notifications).
func (a *ccAdapter) SendText(_ comms.ChannelID, text string) (string, error) {
	if a.ChatDB == nil {
		return "", nil
	}
	convID := a.ChatDB.LatestConversationID("cc")
	if convID == "" {
		convID = "_system"
		a.ChatDB.EnsureConversation(convID, "", "cc")
	}
	msgID := NewMessageID()
	a.ChatDB.InsertMessage(chatdb.Message{
		ID:     msgID,
		ConvID: convID,
		Role:   "assistant",
		Text:   text,
		Source: "cc",
	})
	if a.EventBroker != nil {
		a.EventBroker.EmitWithData(EventNewMessage, fmt.Sprintf(`{"conv_id":%q}`, convID))
	}
	return msgID, nil
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
