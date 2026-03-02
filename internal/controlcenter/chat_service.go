package controlcenter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/mood"
	chatsession "github.com/alamparelli/alf/internal/session"
	"github.com/alamparelli/alf/internal/voice"
)

// RouteResult mirrors RouteResult without importing the router package.
type RouteResult struct {
	Tier     string
	Response string
	Reason   string
	React    string
}

// ClassifyFunc routes a message to a tier. Injected by the daemon.
type ClassifyFunc func(message string, lastTier string, msgCount int) RouteResult

// ResolveModelFunc maps short model names (e.g. "sonnet") to full names.
type ResolveModelFunc func(short string) string

// apiChatID is the session key for API clients. Negative to avoid collision
// with Telegram IDs (always positive for users/groups).
const apiChatID int64 = -1

// ChatService encapsulates the core chat logic: routing, Claude invocation, media handling.
type ChatService struct {
	DataDir      string
	ConfigDir    string
	MemoriesDir  string
	TierStore    TierStore
	Sessions     *chatsession.Store
	EventLog     *eventlog.Logger
	ChatStore    *ChatStore
	Transcriber  *voice.Transcriber // may be nil
	Classify     ClassifyFunc       // injected router
	ResolveModel ResolveModelFunc   // injected model resolver
	mu           sync.Mutex         // serialize Claude calls (single user v1)

	// Upload registry: upload_id → UploadEntry
	uploads   map[string]*UploadEntry
	uploadsMu sync.Mutex
}

// ChatEvent is sent to clients via SSE during streaming.
type ChatEvent struct {
	Type string `json:"type"` // thinking, tool_use, text, reaction, done
	Data any    `json:"data"`
}

// ChatRequest is the input for a chat message.
type ChatRequest struct {
	Message  string   `json:"message"`
	ReplyTo  string   `json:"reply_to,omitempty"`
	MediaIDs []string `json:"media_ids,omitempty"`
	Model    string   `json:"model,omitempty"` // force specific tier/model
}

// ChatDoneData is sent with the "done" event.
type ChatDoneData struct {
	MsgID     string  `json:"msg_id"`
	SessionID string  `json:"session_id"`
	Model     string  `json:"model"`
	CostUSD   float64 `json:"cost_usd"`
	Tier      string  `json:"tier"`
}

// UploadEntry tracks an uploaded file.
type UploadEntry struct {
	ID         string    `json:"upload_id"`
	FileName   string    `json:"file_name"`
	MimeType   string    `json:"mime_type"`
	Size       int64     `json:"size"`
	TempPath   string    `json:"-"`
	MediaType  string    `json:"type"` // photo, document, video, voice
	Transcript string    `json:"transcript,omitempty"`
	TextContent string   `json:"text_content,omitempty"` // extracted text for PDFs
	FramePaths []string  `json:"-"`                       // contact sheet paths for videos
	CreatedAt  time.Time `json:"-"`
}

// UploadResult is returned to the client after upload.
type UploadResult struct {
	UploadID   string `json:"upload_id"`
	FileName   string `json:"file_name"`
	MimeType   string `json:"mime_type"`
	Size       int64  `json:"size"`
	Transcript string `json:"transcript,omitempty"`
}

// ReactRequest is the input for a reaction.
type ReactRequest struct {
	MsgID string `json:"msg_id"`
	Emoji string `json:"emoji"`
}

// ReactResult is returned after processing a reaction.
type ReactResult struct {
	OK     bool   `json:"ok"`
	Mirror string `json:"mirror,omitempty"`
}

// NewChatService creates a new ChatService.
func NewChatService(dataDir, configDir, memoriesDir string, tierStore TierStore, sessions *chatsession.Store, eventLog *eventlog.Logger, chatStore *ChatStore, transcriber *voice.Transcriber, classify ClassifyFunc, resolveModel ResolveModelFunc) *ChatService {
	cs := &ChatService{
		DataDir:      dataDir,
		ConfigDir:    configDir,
		MemoriesDir:  memoriesDir,
		TierStore:    tierStore,
		Sessions:     sessions,
		EventLog:     eventLog,
		ChatStore:    chatStore,
		Transcriber:  transcriber,
		Classify:     classify,
		ResolveModel: resolveModel,
		uploads:      make(map[string]*UploadEntry),
	}
	// Start upload cleanup goroutine.
	go cs.cleanupUploads()
	return cs
}

// RegisterUpload stores an upload entry for later reference.
func (cs *ChatService) RegisterUpload(entry *UploadEntry) {
	cs.uploadsMu.Lock()
	defer cs.uploadsMu.Unlock()
	cs.uploads[entry.ID] = entry
}

// GetUpload returns an upload entry by ID.
func (cs *ChatService) GetUpload(id string) *UploadEntry {
	cs.uploadsMu.Lock()
	defer cs.uploadsMu.Unlock()
	return cs.uploads[id]
}

// Ask processes a chat message, invokes Claude, and streams events via onEvent.
func (cs *ChatService) Ask(ctx context.Context, req ChatRequest, onEvent func(ChatEvent)) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Build prompt from message + reply context + media.
	prompt := cs.buildPrompt(req)
	if prompt == "" {
		return fmt.Errorf("empty message")
	}

	// Save user message.
	userMsg := ChatMessage{
		ID:        NewMessageID(),
		Role:      "user",
		Text:      req.Message,
		Timestamp: time.Now(),
		ReplyTo:   req.ReplyTo,
		Media:     cs.resolveMediaRefs(req.MediaIDs),
	}
	cs.ChatStore.Append(userMsg)

	// Route message.
	hasMedia := len(req.MediaIDs) > 0
	var tierName string
	var routeResult RouteResult

	if req.Model != "" {
		// Force specific model — find matching tier or use raw model.
		tierName = req.Model
		routeResult = RouteResult{Tier: tierName, Reason: "forced"}
	} else if hasMedia {
		// Media bypass router — pick lowest-priority enabled tier.
		tiers := cs.TierStore.Current()
		bestPriority := int(^uint(0) >> 1)
		for _, t := range tiers.Tiers {
			if t.Enabled && t.Priority < bestPriority {
				tierName = t.Name
				bestPriority = t.Priority
			}
		}
		if tierName == "" && len(tiers.Tiers) > 0 {
			tierName = tiers.Tiers[0].Name
		}
		routeResult = RouteResult{Tier: tierName, Reason: "media bypass"}
	} else {
		// Router classify.
		routerMsg := req.Message
		if req.ReplyTo != "" {
			if orig := cs.ChatStore.Get(req.ReplyTo); orig != nil {
				quoted := orig.Text
				if len(quoted) > 100 {
					quoted = quoted[:100] + "..."
				}
				routerMsg = fmt.Sprintf("[Replying to: \"%s\"]\n%s", quoted, req.Message)
			}
		}

		lastTier, msgCount := cs.Sessions.Context(apiChatID) // chatID 0 for API
		rr := cs.Classify(routerMsg, lastTier, msgCount)
		routeResult = rr
	}

	// Router direct response.
	if routeResult.Response != "" && routeResult.Tier == "" {
		cs.Sessions.TouchContext(apiChatID, "router")
		if routeResult.React != "" {
			onEvent(ChatEvent{Type: "reaction", Data: map[string]string{"emoji": routeResult.React}})
		}
		assistantMsg := ChatMessage{
			ID:        NewMessageID(),
			Role:      "assistant",
			Text:      routeResult.Response,
			Timestamp: time.Now(),
			Model:     "router",
			Tier:      "router",
		}
		cs.ChatStore.Append(assistantMsg)
		onEvent(ChatEvent{Type: "text", Data: map[string]string{"text": routeResult.Response}})
		onEvent(ChatEvent{Type: "done", Data: ChatDoneData{
			MsgID: assistantMsg.ID,
			Model: "router",
			Tier:  "router",
		}})
		return nil
	}

	tierName = routeResult.Tier
	tp := cs.resolveTierParams(tierName)

	cs.EventLog.Log("router_classify", map[string]any{
		"tier":   tierName,
		"reason": routeResult.Reason,
		"model":  tp.Model,
		"source": "api",
	})

	// Invoke Claude via CLI.
	resumeID := cs.Sessions.Get(apiChatID)
	result, err := cs.askClaude(ctx, prompt, resumeID, tp, func(event, detail string) {
		switch event {
		case "thinking":
			onEvent(ChatEvent{Type: "thinking", Data: struct{}{}})
		case "tool_use":
			onEvent(ChatEvent{Type: "tool_use", Data: map[string]string{"name": detail}})
		case "text":
			// text events are accumulated and sent per-delta below
		}
	})

	// Retry without resume if session not found.
	if err != nil && resumeID != "" && strings.Contains(err.Error(), "No conversation found") {
		log.Printf("[chat-api] session %s expired, starting fresh", resumeID)
		cs.Sessions.Archive(apiChatID)
		result, err = cs.askClaude(ctx, prompt, "", tp, nil)
	}

	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}

	// Update session.
	if result.SessionID != "" {
		cs.Sessions.SetWithContext(apiChatID, result.SessionID, tierName)
	}

	// Extract reaction.
	suggestedEmoji, cleanText := extractReactionTag(result.Text)
	if suggestedEmoji != "" {
		emoji := mood.ValidateOrFallback(suggestedEmoji)
		if emoji != "" {
			onEvent(ChatEvent{Type: "reaction", Data: map[string]string{"emoji": emoji}})
		}
	}

	// Maybe spontaneous react via mood system.
	state := mood.GetCurrentState(cs.MemoriesDir)
	if mood.ShouldReact(state) {
		spontaneous := mood.ChooseSpontaneous(state)
		if spontaneous != "" {
			onEvent(ChatEvent{Type: "reaction", Data: map[string]string{"emoji": spontaneous}})
		}
	}

	// Save assistant message.
	assistantMsg := ChatMessage{
		ID:        NewMessageID(),
		Role:      "assistant",
		Text:      cleanText,
		Timestamp: time.Now(),
		Model:     result.Model,
		Tier:      tierName,
		CostUSD:   result.CostUSD,
		SessionID: result.SessionID,
	}
	cs.ChatStore.Append(assistantMsg)

	// Send text to client.
	onEvent(ChatEvent{Type: "text", Data: map[string]string{"text": cleanText}})

	onEvent(ChatEvent{Type: "done", Data: ChatDoneData{
		MsgID:     assistantMsg.ID,
		SessionID: result.SessionID,
		Model:     result.Model,
		CostUSD:   result.CostUSD,
		Tier:      tierName,
	}})

	cs.EventLog.Log("message_out", map[string]any{
		"model":       result.Model,
		"cost_usd":    result.CostUSD,
		"text_length": len(cleanText),
		"session_id":  result.SessionID,
		"tier":        tierName,
		"source":      "api",
	})

	return nil
}

// React processes a user reaction on a message.
func (cs *ChatService) React(req ReactRequest) (*ReactResult, error) {
	emoji := mood.ValidateOrFallback(req.Emoji)
	if emoji == "" {
		return nil, fmt.Errorf("invalid emoji")
	}

	mood.LogReaction(cs.DataDir, emoji, 0)
	mood.UpdateLiveFeedback(cs.MemoriesDir, cs.DataDir)

	cs.ChatStore.AddReaction(req.MsgID, Reaction{Emoji: emoji, From: "user"})

	result := &ReactResult{OK: true}

	_, state := mood.GetTodayScore(cs.DataDir)
	if mood.ShouldReact(state) {
		mirror := mood.ChooseMirror(emoji, state)
		if mirror != "" {
			result.Mirror = mirror
			cs.ChatStore.AddReaction(req.MsgID, Reaction{Emoji: mirror, From: "alf"})
		}
	}

	// Async negative follow-up.
	if mood.IsNegative(emoji) {
		go cs.negativeFollowUp(emoji, req.MsgID)
	}

	return result, nil
}

// History returns paginated chat history.
func (cs *ChatService) History(limit int, before time.Time) []ChatMessage {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	msgs := cs.ChatStore.History(limit, before)
	if msgs == nil {
		return []ChatMessage{}
	}
	return msgs
}

// buildPrompt constructs the full prompt from a ChatRequest.
func (cs *ChatService) buildPrompt(req ChatRequest) string {
	var parts []string

	// Reply context.
	if req.ReplyTo != "" {
		if orig := cs.ChatStore.Get(req.ReplyTo); orig != nil {
			parts = append(parts, fmt.Sprintf("[The user is replying to this previous message:\n---\n%s\n---\n]", orig.Text))
		}
	}

	// Media context.
	for _, mid := range req.MediaIDs {
		entry := cs.GetUpload(mid)
		if entry == nil {
			continue
		}
		switch {
		case media.IsImageContent(entry.MimeType):
			parts = append(parts, fmt.Sprintf("[PHOTO — use Read tool to view: %s]", entry.TempPath))
		case media.IsVideoContent(entry.MimeType, entry.FileName):
			if len(entry.FramePaths) > 0 {
				parts = append(parts, fmt.Sprintf("[VIDEO \"%s\" — contact sheet with key frames. Use Read tool to view: %s]", entry.FileName, strings.Join(entry.FramePaths, ", ")))
			} else {
				parts = append(parts, fmt.Sprintf("[VIDEO \"%s\" — use Read tool to view: %s]", entry.FileName, entry.TempPath))
			}
			if entry.Transcript != "" {
				parts = append(parts, fmt.Sprintf("[Audio transcript: %s]", entry.Transcript))
			}
		case entry.MediaType == "voice":
			parts = append(parts, fmt.Sprintf("[Voice message transcript: %s]", entry.Transcript))
		case media.IsTextContent(entry.MimeType) || media.IsPDFContent(entry.MimeType):
			if entry.TextContent != "" {
				parts = append(parts, fmt.Sprintf("[FILE: %s]\nContent:\n%s", entry.FileName, entry.TextContent))
			} else {
				parts = append(parts, fmt.Sprintf("[FILE: %s — use Read tool to view: %s]", entry.FileName, entry.TempPath))
			}
		default:
			parts = append(parts, fmt.Sprintf("[FILE: %s — use Read tool to view: %s]", entry.FileName, entry.TempPath))
		}
	}

	// User text.
	if req.Message != "" {
		parts = append(parts, req.Message)
	}

	return strings.Join(parts, "\n")
}

func (cs *ChatService) resolveMediaRefs(ids []string) []MediaRef {
	var refs []MediaRef
	for _, id := range ids {
		entry := cs.GetUpload(id)
		if entry == nil {
			continue
		}
		refs = append(refs, MediaRef{
			UploadID: entry.ID,
			Type:     entry.MediaType,
			FileName: entry.FileName,
			MimeType: entry.MimeType,
			URL:      "/api/chat/media/" + entry.ID,
		})
	}
	return refs
}

// resolveTierParams looks up tier config and returns CLI parameters.
func (cs *ChatService) resolveTierParams(tierName string) tierParams {
	tiers := cs.TierStore.Current()
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			model := t.Model
			if cs.ResolveModel != nil {
				model = cs.ResolveModel(t.Model)
			}
			return tierParams{
				Model:  model,
				Tools:  t.Tools,
				Effort: t.Effort,
			}
		}
	}
	fallback := "claude-haiku-4-5"
	if cs.ResolveModel != nil {
		fallback = cs.ResolveModel("haiku")
	}
	return tierParams{Model: fallback}
}

// tierParams holds per-tier Claude CLI arguments.
type tierParams struct {
	Model  string
	Tools  []string
	Effort string
}

// claudeResult holds parsed output from Claude CLI.
type claudeResult struct {
	SessionID string
	Text      string
	Model     string
	CostUSD   float64
	NumTurns  int
}

type jsonModel struct {
	CostUSD float64 `json:"costUSD"`
}

// reactionSystemPromptTmpl is the reaction instruction injected into every Claude call.
const reactionSystemPromptTmpl = `You may optionally suggest a single emoji reaction for the user's message by starting your response with [[react:EMOJI]]. Pick an emoji that shows you understood the message — not generic thumbs up. Use [[react:none]] or omit the tag if no reaction fits. The tag will be stripped before the user sees your response.
IMPORTANT: You MUST only use one of these Telegram-allowed reaction emoji: %s`

// askClaude invokes the Claude CLI as a subprocess with stream-json output.
func (cs *ChatService) askClaude(ctx context.Context, prompt, resumeID string, tp tierParams, onProgress func(event, detail string)) (*claudeResult, error) {
	model := tp.Model
	if model == "" {
		model = "claude-haiku-4-5"
	}

	safePrompt := prompt
	if strings.HasPrefix(prompt, "-") {
		safePrompt = "\u200B" + prompt
	}

	args := []string{
		"-p", safePrompt,
		"--model", model,
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
	}

	for _, tool := range tp.Tools {
		args = append(args, "--allowedTools", tool)
	}
	if tp.Effort != "" {
		args = append(args, "--effort", tp.Effort)
	}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}

	// Inject memory files.
	args = append(args, memory.CollectPrompts(cs.MemoriesDir)...)

	// Reaction instruction.
	args = append(args, "--append-system-prompt", fmt.Sprintf(reactionSystemPromptTmpl, mood.AllowedReactionList()))

	timeout := 5 * time.Minute
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "claude", args...)
	cmd.Dir = cs.DataDir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1001, Gid: 1000},
	}
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "HOME=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "HOME="+cs.DataDir)

	log.Printf("[chat-api] askClaude: starting (resume=%q, model=%s)", resumeID, model)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	var (
		resultText   strings.Builder
		lastEvent    json.RawMessage
		sentThinking bool
	)

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		lastEvent = make(json.RawMessage, len(line))
		copy(lastEvent, line)

		var event struct {
			Type  string `json:"type"`
			Event struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				ContentBlock struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content_block"`
			} `json:"event"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}

		if onProgress != nil {
			switch {
			case event.Type == "stream_event" && event.Event.ContentBlock.Type == "thinking" && !sentThinking:
				onProgress("thinking", "")
				sentThinking = true
			case event.Type == "stream_event" && event.Event.ContentBlock.Type == "tool_use":
				onProgress("tool_use", event.Event.ContentBlock.Name)
			case event.Type == "stream_event" && event.Event.Delta.Type == "text_delta":
				onProgress("text", "")
			}
		}

		if event.Type == "stream_event" && event.Event.Delta.Type == "text_delta" {
			resultText.WriteString(event.Event.Delta.Text)
		}
	}

	waitErr := cmd.Wait()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("claude timed out after 5 minutes")
	}

	if lastEvent != nil {
		var parsed struct {
			Type         string               `json:"type"`
			SessionID    string               `json:"session_id"`
			Subtype      string               `json:"subtype"`
			Result       string               `json:"result"`
			IsError      bool                 `json:"is_error"`
			NumTurns     int                  `json:"num_turns"`
			TotalCostUSD float64              `json:"total_cost_usd"`
			ModelUsage   map[string]jsonModel `json:"modelUsage"`
		}
		if json.Unmarshal(lastEvent, &parsed) == nil && parsed.Type == "result" {
			text := parsed.Result
			if text == "" {
				text = resultText.String()
			}
			if text == "" {
				switch parsed.Subtype {
				case "error_max_turns":
					text = "Turn limit reached — try breaking this into smaller steps."
				default:
					if parsed.IsError {
						text = "An error occurred processing your request."
					} else {
						text = "Done (no text output)."
					}
				}
			}
			if parsed.IsError && strings.Contains(text, "No conversation found") {
				return nil, fmt.Errorf("claude: %s", text)
			}
			usedModel := "unknown"
			for m := range parsed.ModelUsage {
				usedModel = m
				break
			}
			return &claudeResult{
				SessionID: parsed.SessionID,
				Text:      text,
				Model:     usedModel,
				CostUSD:   parsed.TotalCostUSD,
				NumTurns:  parsed.NumTurns,
			}, nil
		}
	}

	accumulated := strings.TrimSpace(resultText.String())
	if accumulated != "" {
		return &claudeResult{Text: accumulated}, nil
	}

	errOut := strings.TrimSpace(stderr.String())
	if waitErr != nil {
		if errOut != "" {
			return nil, fmt.Errorf("claude: %s", errOut)
		}
		return nil, fmt.Errorf("claude failed: %v", waitErr)
	}

	return nil, fmt.Errorf("claude returned empty response")
}

// extractReactionTag parses [[react:EMOJI]] from the start of text.
func extractReactionTag(text string) (string, string) {
	trimmed := strings.TrimLeft(text, " \n\r\t")
	if !strings.HasPrefix(trimmed, "[[react:") {
		return "", text
	}
	end := strings.Index(trimmed, "]]")
	if end == -1 {
		return "", text
	}
	emoji := trimmed[len("[[react:"):end]
	rest := strings.TrimLeft(trimmed[end+2:], " \n\r\t")
	if emoji == "none" || emoji == "" {
		return "", rest
	}
	return emoji, rest
}

// negativeFollowUp sends a Claude message asking about negative feedback.
func (cs *ChatService) negativeFollowUp(emoji, msgID string) {
	time.Sleep(2 * time.Second)

	cs.mu.Lock()
	defer cs.mu.Unlock()

	langNote := "IMPORTANT: Reply in the same language the user has been using in this conversation."
	var prompt string
	if mood.IsStrongNegative(emoji) {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (strong negative). Something is clearly wrong. Acknowledge the negative feedback briefly, identify what likely went wrong in your previous response, and ask a short direct question to understand what they expected. Keep it to 2-3 sentences max. Don't be defensive. %s", emoji, langNote)
	} else {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (mild negative). Briefly acknowledge the feedback and ask a short question to understand what could be improved. One or two sentences max. Stay casual. %s", emoji, langNote)
	}

	resumeID := cs.Sessions.Get(apiChatID)
	tp := tierParams{Model: "claude-haiku-4-5"}
	for _, t := range cs.TierStore.Current().Tiers {
		if t.Instant {
			tp = cs.resolveTierParams(t.Name)
			break
		}
	}

	result, err := cs.askClaude(context.Background(), prompt, resumeID, tp, nil)
	if err != nil {
		log.Printf("[chat-api] negative follow-up error: %v", err)
		return
	}

	if result.SessionID != "" {
		cs.Sessions.SetWithContext(apiChatID, result.SessionID, "follow-up")
	}

	_, cleanText := extractReactionTag(result.Text)
	followUpMsg := ChatMessage{
		ID:        NewMessageID(),
		Role:      "assistant",
		Text:      cleanText,
		Timestamp: time.Now(),
		Model:     result.Model,
		Tier:      "follow-up",
	}
	cs.ChatStore.Append(followUpMsg)

	cs.EventLog.Log("negative_followup", map[string]any{
		"emoji":  emoji,
		"model":  result.Model,
		"source": "api",
	})
}

// cleanupUploads periodically removes expired upload entries.
func (cs *ChatService) cleanupUploads() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cs.uploadsMu.Lock()
		now := time.Now()
		for id, entry := range cs.uploads {
			if now.Sub(entry.CreatedAt) > 10*time.Minute {
				os.Remove(entry.TempPath)
				for _, p := range entry.FramePaths {
					os.Remove(p)
				}
				delete(cs.uploads, id)
			}
		}
		cs.uploadsMu.Unlock()
	}
}

// Upload processes an uploaded file: saves to temp, detects type, transcribes if needed.
func (cs *ChatService) Upload(file io.Reader, fileName, mediaType string) (*UploadResult, error) {
	// Sanitize filename: strip path components and dangerous characters.
	fileName = filepath.Base(fileName)
	fileName = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '"' || r == '\x00' {
			return '_'
		}
		return r
	}, fileName)

	// Validate media type.
	switch mediaType {
	case "photo", "document", "video", "voice":
	default:
		mediaType = "document"
	}

	data, err := io.ReadAll(io.LimitReader(file, 50*1024*1024)) // 50MB max
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}

	mimeType := media.DetectMimeType(data, fileName)
	ext := extFromMimeMap(mimeType, fileName)

	tmpFile, err := os.CreateTemp("", "alf-media-*"+ext)
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("close temp file: %w", err)
	}
	os.Chmod(tmpFile.Name(), 0o644)

	entry := &UploadEntry{
		ID:        NewMessageID(),
		FileName:  fileName,
		MimeType:  mimeType,
		Size:      int64(len(data)),
		TempPath:  tmpFile.Name(),
		MediaType: mediaType,
		CreatedAt: time.Now(),
	}

	// Process based on type.
	switch {
	case mediaType == "voice" && cs.Transcriber != nil && cs.Transcriber.IsReady():
		result, err := cs.Transcriber.Transcribe(tmpFile.Name())
		if err != nil {
			log.Printf("[chat-api] voice transcription failed: %v", err)
		} else {
			entry.Transcript = result.Text
		}

	case media.IsVideoContent(mimeType, fileName):
		maxFrames := 8
		frames, err := media.ExtractFrames(tmpFile.Name(), maxFrames)
		if err != nil {
			log.Printf("[chat-api] frame extraction failed: %v", err)
		} else {
			entry.FramePaths = frames
		}
		// Try audio transcription.
		if cs.Transcriber != nil && cs.Transcriber.IsReady() {
			audioPath, err := media.ExtractAudio(tmpFile.Name())
			if err == nil && audioPath != "" {
				result, err := cs.Transcriber.Transcribe(audioPath)
				if err == nil && result.Text != "" {
					entry.Transcript = result.Text
				}
				os.Remove(audioPath)
			}
		}

	case media.IsTextContent(mimeType) || media.IsPDFContent(mimeType):
		entry.TextContent = media.ExtractTextFromDocument(data, mimeType)
	}

	cs.RegisterUpload(entry)

	return &UploadResult{
		UploadID:   entry.ID,
		FileName:   entry.FileName,
		MimeType:   entry.MimeType,
		Size:       entry.Size,
		Transcript: entry.Transcript,
	}, nil
}

// extFromMimeMap returns a file extension for a MIME type.
func extFromMimeMap(mimeType, fileName string) string {
	mimeToExt := map[string]string{
		"image/jpeg":       ".jpg",
		"image/png":        ".png",
		"image/gif":        ".gif",
		"image/webp":       ".webp",
		"application/pdf":  ".pdf",
		"video/mp4":        ".mp4",
		"video/quicktime":  ".mov",
		"video/webm":       ".webm",
		"audio/ogg":        ".ogg",
		"audio/mpeg":       ".mp3",
		"audio/wav":        ".wav",
	}
	if ext, ok := mimeToExt[mimeType]; ok {
		return ext
	}
	if ext := filepath.Ext(fileName); ext != "" {
		return ext
	}
	return ""
}
