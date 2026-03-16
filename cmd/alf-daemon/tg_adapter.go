package main

import (
	"sync"

	"github.com/alamparelli/alf/internal/comms"
	tgclient "github.com/alamparelli/alf/internal/telegram"
)

// tgAdapter bridges comms.ChannelAdapter to Telegram-specific I/O.
// Manages per-channel typing indicators during engine.Process() calls.
type tgAdapter struct {
	tg         *tgclient.Client
	mu         sync.Mutex
	indicators map[comms.ChannelID]*typingIndicator
}

func newTGAdapter(tg *tgclient.Client) *tgAdapter {
	return &tgAdapter{
		tg:         tg,
		indicators: make(map[comms.ChannelID]*typingIndicator),
	}
}

func (a *tgAdapter) Channel() string { return "tg" }

func (a *tgAdapter) SendText(channelID comms.ChannelID, text string) (string, error) {
	chatID := channelID.SessionKey()
	if chatID <= 0 {
		return "", nil
	}
	_, err := a.tg.SendMessageReturnID(chatID, text)
	return "", err
}

func (a *tgAdapter) SendReaction(channelID comms.ChannelID, msgID string, emoji string) error {
	return nil // reactions handled explicitly in the TG loop
}

// OnEvent updates typing indicators based on engine streaming events.
// The "done" event is suppressed — the TG loop handles completion explicitly.
func (a *tgAdapter) OnEvent(channelID comms.ChannelID, event comms.OutEvent) {
	a.mu.Lock()
	ind := a.indicators[channelID]
	a.mu.Unlock()
	if ind == nil {
		return
	}

	switch event.Type {
	case "thinking":
		ind.SetAction("choose_sticker")
	case "tool_use":
		ind.SetAction("upload_document")
	case "tool_input":
		ind.SetAction("upload_document")
	case "text_delta":
		ind.SetAction("typing")
	case "agent_start", "planning", "agent_tool":
		ind.SetAction("upload_document")
	case "agent_thinking":
		ind.SetAction("choose_sticker")
	case "synthesizing":
		ind.SetAction("typing")
	case "done":
		// Suppressed: TG loop stops the indicator explicitly after sending the message.
	}
}

// SetIndicator registers a typing indicator for a channel.
func (a *tgAdapter) SetIndicator(channelID comms.ChannelID, ind *typingIndicator) {
	a.mu.Lock()
	a.indicators[channelID] = ind
	a.mu.Unlock()
}

// ClearIndicator removes the typing indicator for a channel.
func (a *tgAdapter) ClearIndicator(channelID comms.ChannelID) {
	a.mu.Lock()
	delete(a.indicators, channelID)
	a.mu.Unlock()
}
