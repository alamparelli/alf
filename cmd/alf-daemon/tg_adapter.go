package main

import (
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alamparelli/alf/internal/runtime/comms"
	tgclient "github.com/alamparelli/alf/internal/telegram"
)

// mediaExts maps file extensions to their Telegram send method category.
var videoExts = map[string]bool{".mp4": true, ".webm": true, ".mov": true}
var animExts = map[string]bool{".gif": true, ".webp": true}

// sendTGNotify sends a notification to Telegram, detecting media URLs/paths
// and using SendAnimation/SendVideo for GIFs and videos.
// Supports: bare URL, bare file path, or "url/path\ncaption" format.
func sendTGNotify(tg *tgclient.Client, chatID int64, text string) error {
	trimmed := strings.TrimSpace(text)

	// Split into first line (potential media ref) and rest (caption).
	first, caption := trimmed, ""
	if lines := strings.SplitN(trimmed, "\n", 2); len(lines) == 2 {
		first = strings.TrimSpace(lines[0])
		caption = strings.TrimSpace(lines[1])
	}

	// Must be a single token (no spaces) to be a media reference.
	if !strings.Contains(first, " ") {
		ext := strings.ToLower(filepath.Ext(first))
		isURL := strings.HasPrefix(first, "http://") || strings.HasPrefix(first, "https://")
		isFile := strings.HasPrefix(first, "/")

		if isURL {
			if animExts[ext] {
				return tg.SendAnimation(chatID, first, caption)
			}
			if videoExts[ext] {
				return tg.SendVideo(chatID, first, caption)
			}
		} else if isFile {
			// Local file path — upload via multipart.
			if animExts[ext] {
				return tg.SendAnimationFile(chatID, first, caption)
			}
			if videoExts[ext] {
				return tg.SendVideoFile(chatID, first, caption)
			}
			// Other file types → send as document.
			if ext != "" {
				return tg.SendDocumentFile(chatID, first, caption)
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
