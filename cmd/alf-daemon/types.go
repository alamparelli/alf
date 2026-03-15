package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// tierParams holds per-tier Claude CLI arguments.
type tierParams struct {
	Model                string   // full model name, e.g. "claude-sonnet-4-5"
	Tools                []string // nil = omit flag
	WriteCapable         bool     // if true, grants full tool access; if false, restricts to Tools whitelist
	Effort               string   // "" = omit flag
	MaxTurns             int      // 0 = omit flag (use Claude default)
	OrchestratorMaxTurns int      // turns per orchestrator brain call (0 = default 3)
	MaxIterations        int      // max agent iterations (0 = default)
	TimeoutMin           int      // global timeout in minutes (0 = default)
	Backend              string   // "cli" (default), or registered backend name
	SystemPrompt         string   // extra system prompt for this tier
}

type Update struct {
	UpdateID        int64                   `json:"update_id"`
	Message         *Message                `json:"message"`
	CallbackQuery   *CallbackQuery          `json:"callback_query"`
	MessageReaction *MessageReactionUpdated `json:"message_reaction"`
}

type Message struct {
	MessageID       int64      `json:"message_id"`
	Chat            Chat       `json:"chat"`
	From            User       `json:"from"`
	Text            string     `json:"text"`
	ReplyToMessage  *Message   `json:"reply_to_message"`
	Photo           []*Photo   `json:"photo"`
	Document        *Document  `json:"document"`
	Video           *Video     `json:"video"`
	Animation       *Animation `json:"animation"`
	Audio           *Audio     `json:"audio"`
	Voice           *Voice     `json:"voice"`
	VideoNote       *VideoNote `json:"video_note"`
	Caption         string     `json:"caption"`
	MediaGroupID    string     `json:"media_group_id"`
	extraFiles      []mediaFile // populated by mergeMediaGroups for multi-file albums
}

type mediaFile struct {
	FileID   string
	FileName string
}

type Photo struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type Video struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type Audio struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type Voice struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type VideoNote struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type Animation struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type MessageReactionUpdated struct {
	Chat        Chat           `json:"chat"`
	MessageID   int64          `json:"message_id"`
	User        *User          `json:"user"`
	NewReaction []ReactionType `json:"new_reaction"`
}

type ReactionType struct {
	Type  string `json:"type"`
	Emoji string `json:"emoji"`
}

type CallbackQuery struct {
	ID   string  `json:"id"`
	From User    `json:"from"`
	Data string  `json:"data"`
	Message *CBMessage `json:"message"`
}

type CBMessage struct {
	Chat Chat `json:"chat"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// ringBuffer is a fixed-capacity ring buffer for tracking message IDs.
type ringBuffer struct {
	mu   sync.Mutex
	data []int64
	pos  int
	full bool
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{data: make([]int64, capacity)}
}

func (r *ringBuffer) Add(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.pos] = id
	r.pos = (r.pos + 1) % len(r.data)
	if r.pos == 0 {
		r.full = true
	}
}

func (r *ringBuffer) Contains(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	limit := len(r.data)
	if !r.full {
		limit = r.pos
	}
	for i := 0; i < limit; i++ {
		if r.data[i] == id {
			return true
		}
	}
	return false
}

func (r *ringBuffer) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return len(r.data)
	}
	return r.pos
}

// chatHistoryBuffer stores recent message exchanges per chat for context injection.
type chatHistoryBuffer struct {
	mu      sync.Mutex
	history map[int64][]chatEntry
	maxSize int
}

type chatEntry struct {
	Role string // "user" or "alf"
	Text string
}

func newChatHistoryBuffer(maxPerChat int) *chatHistoryBuffer {
	return &chatHistoryBuffer{
		history: make(map[int64][]chatEntry),
		maxSize: maxPerChat,
	}
}

func (h *chatHistoryBuffer) Add(chatID int64, role, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Truncate long messages for context summary.
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	entries := h.history[chatID]
	entries = append(entries, chatEntry{Role: role, Text: text})
	if len(entries) > h.maxSize {
		entries = entries[len(entries)-h.maxSize:]
	}
	h.history[chatID] = entries
}

func (h *chatHistoryBuffer) Recent(chatID int64, n int) []chatEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := h.history[chatID]
	if len(entries) <= n {
		return append([]chatEntry{}, entries...)
	}
	return append([]chatEntry{}, entries[len(entries)-n:]...)
}

func (h *chatHistoryBuffer) Clear(chatID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.history, chatID)
}

func getUpdates(client *http.Client, token string, offset int64) ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s", token, offset, `["message","callback_query","message_reaction"]`)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	const maxUpdatesBody = 10 * 1024 * 1024 // 10MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdatesBody))
	if err != nil {
		return nil, fmt.Errorf("read getUpdates body: %w", err)
	}

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram API error: %s", string(body))
	}
	return result.Result, nil
}

// mergeMediaGroups consolidates updates that share the same media_group_id
// into a single update with multiple file references. This ensures albums
// (multiple photos/documents sent together) are processed as one message.
func mergeMediaGroups(updates []Update) []Update {
	var merged []Update
	seen := make(map[string]int) // media_group_id → index in merged

	for _, u := range updates {
		if u.Message == nil || u.Message.MediaGroupID == "" {
			merged = append(merged, u)
			continue
		}

		gid := u.Message.MediaGroupID
		if idx, ok := seen[gid]; ok {
			// Merge into existing: append photos/documents from this message.
			target := merged[idx].Message
			if len(u.Message.Photo) > 0 {
				target.Photo = append(target.Photo, u.Message.Photo...)
			}
			if u.Message.Document != nil {
				// Store additional documents as extra photos workaround:
				// we'll handle multi-doc via extraFiles below.
				if target.extraFiles == nil {
					target.extraFiles = []mediaFile{}
				}
				target.extraFiles = append(target.extraFiles, mediaFile{
					FileID:   u.Message.Document.FileID,
					FileName: u.Message.Document.FileName,
				})
			}
			if u.Message.Video != nil {
				if target.extraFiles == nil {
					target.extraFiles = []mediaFile{}
				}
				target.extraFiles = append(target.extraFiles, mediaFile{
					FileID:   u.Message.Video.FileID,
					FileName: u.Message.Video.FileName,
				})
			}
			// Use caption from whichever message has one.
			if target.Caption == "" && u.Message.Caption != "" {
				target.Caption = u.Message.Caption
			}
		} else {
			seen[gid] = len(merged)
			merged = append(merged, u)
		}
	}
	return merged
}
