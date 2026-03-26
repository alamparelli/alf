package main

import (
	"log"
	"strings"
	"sync"

	"github.com/alamparelli/alf/internal/comms"
	tgclient "github.com/alamparelli/alf/internal/telegram"
)

// sendTGNotify sends a notification to Telegram, detecting media URLs
// and using SendAnimation/SendVideo for GIFs and videos.
func sendTGNotify(tg *tgclient.Client, chatID int64, text string) error {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	// If the text is just a URL ending in a media extension, send as media.
	if (strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")) && !strings.Contains(trimmed, " ") {
		switch {
		case strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".webp"):
			return tg.SendAnimation(chatID, trimmed, "")
		case strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".webm") || strings.HasSuffix(lower, ".mov"):
			return tg.SendVideo(chatID, trimmed, "")
		}
	}

	// Check for "url\ncaption" pattern (media URL on first line, caption on rest).
	if lines := strings.SplitN(trimmed, "\n", 2); len(lines) == 2 {
		url := strings.TrimSpace(lines[0])
		caption := strings.TrimSpace(lines[1])
		urlLower := strings.ToLower(url)
		if strings.HasPrefix(url, "http") && !strings.Contains(url, " ") {
			switch {
			case strings.HasSuffix(urlLower, ".gif") || strings.HasSuffix(urlLower, ".webp"):
				return tg.SendAnimation(chatID, url, caption)
			case strings.HasSuffix(urlLower, ".mp4") || strings.HasSuffix(urlLower, ".webm") || strings.HasSuffix(urlLower, ".mov"):
				return tg.SendVideo(chatID, url, caption)
			}
		}
	}

	return tg.SendMessage(chatID, text)
}

// tgAdapter bridges comms.ChannelAdapter to Telegram-specific I/O.
// Manages per-channel typing indicators during engine.Process() calls.
type tgAdapter struct {
	tg               *tgclient.Client
	mu               sync.Mutex
	indicators       map[comms.ChannelID]*typingIndicator
	broadcastTargets []int64 // chat IDs to send broadcasts to (from allowedChatIDs)
}

func newTGAdapter(tg *tgclient.Client) *tgAdapter {
	return &tgAdapter{
		tg:         tg,
		indicators: make(map[comms.ChannelID]*typingIndicator),
	}
}

// SetBroadcastTargets configures the chat IDs that receive broadcast messages.
func (a *tgAdapter) SetBroadcastTargets(ids []int64) {
	a.broadcastTargets = ids
}

func (a *tgAdapter) Channel() string { return "tg" }

func (a *tgAdapter) SendText(channelID comms.ChannelID, text string) (string, error) {
	chatID := channelID.SessionKey()
	if chatID <= 0 {
		// Invalid channel ID — broadcast to all configured targets.
		for _, id := range a.broadcastTargets {
			if _, err := a.tg.SendMessageReturnID(id, text); err != nil {
				log.Printf("[tg] broadcast to %d failed: %v", id, err)
			}
		}
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
