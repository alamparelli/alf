package controlcenter

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/mood"
	"github.com/alamparelli/alf/internal/provider"
	chatsession "github.com/alamparelli/alf/internal/session"
	"github.com/alamparelli/alf/internal/skills"
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

// MemoryRecaller searches long-term memory by semantic similarity.
type MemoryRecaller interface {
	Search(query string, limit int) ([]MemoryResult, error)
}

// MemoryResult is a single memory search hit.
type MemoryResult struct {
	Text     string
	Type     string
	Distance float64
}

// ChatService encapsulates the core chat logic: routing, Claude invocation, media handling.
type ChatService struct {
	DataDir      string
	ConfigDir    string
	ContextDir   string
	TierStore    TierStore
	Sessions     *chatsession.Store
	EventLog     *eventlog.Logger
	ChatStore    *ChatStore
	Transcriber  *voice.Transcriber   // may be nil
	Classify     ClassifyFunc         // injected router
	ResolveModel ResolveModelFunc     // injected model resolver
	Provider     provider.Provider    // injected Claude provider (default)
	Registry     *provider.Registry   // may be nil — multi-backend dispatch
	APIHistory   *provider.History    // may be nil — API provider history
	Recaller     MemoryRecaller       // may be nil — auto-injects relevant memories
	SkillStore   skills.Store         // may be nil — injects skill catalog into system prompts
	Orchestrator *agents.Orchestrator // may be nil — multi-agent orchestrator
	mu           sync.Mutex           // serialize Claude calls (single user v1)

	// Background job tracking.
	activeJob *chatJob
	jobMu     sync.Mutex

	// Upload registry: upload_id → UploadEntry
	uploads   map[string]*UploadEntry
	uploadsMu sync.Mutex
}

// ChatEvent is sent to clients via SSE during streaming.
type ChatEvent struct {
	Type string `json:"type"` // thinking, tool_use, text_delta, text, reaction, done
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
	ID          string    `json:"upload_id"`
	FileName    string    `json:"file_name"`
	MimeType    string    `json:"mime_type"`
	Size        int64     `json:"size"`
	TempPath    string    `json:"-"`
	MediaType   string    `json:"type"` // photo, document, video, voice
	Transcript  string    `json:"transcript,omitempty"`
	TextContent string    `json:"text_content,omitempty"` // extracted text for PDFs
	FramePaths  []string  `json:"-"`                      // contact sheet paths for videos
	CreatedAt   time.Time `json:"-"`
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

// reactionSystemPromptTmpl is the reaction instruction injected into every Claude call.
const reactionSystemPromptTmpl = `You may optionally suggest a single emoji reaction for the user's message by starting your response with [[react:EMOJI]]. Pick an emoji that shows you understood the message — not generic thumbs up. Use [[react:none]] or omit the tag if no reaction fits. The tag will be stripped before the user sees your response.
IMPORTANT: You MUST only use one of these Telegram-allowed reaction emoji: %s`

// NewChatService creates a new ChatService.
func NewChatService(dataDir, configDir, contextDir string, tierStore TierStore, sessions *chatsession.Store, eventLog *eventlog.Logger, chatStore *ChatStore, transcriber *voice.Transcriber, classify ClassifyFunc, resolveModel ResolveModelFunc, prov provider.Provider) *ChatService {
	cs := &ChatService{
		DataDir:      dataDir,
		ConfigDir:    configDir,
		ContextDir:  contextDir,
		TierStore:    tierStore,
		Sessions:     sessions,
		EventLog:     eventLog,
		ChatStore:    chatStore,
		Transcriber:  transcriber,
		Classify:     classify,
		ResolveModel: resolveModel,
		Provider:     prov,
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

	// Detect force command: /<tier_name> <message> bypasses routing.
	if strings.HasPrefix(req.Message, "/") && req.Model == "" {
		parts := strings.SplitN(req.Message, " ", 2)
		cmdName := strings.TrimPrefix(parts[0], "/")
		for _, t := range cs.TierStore.Current().Tiers {
			if t.Enabled && t.ForceCommand && t.Name == cmdName {
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					return fmt.Errorf("Usage: /%s <message>", t.Name)
				}
				req.Model = t.Name
				req.Message = strings.TrimSpace(parts[1])
				break
			}
		}
	}

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
		tierName = req.Model
		routeResult = RouteResult{Tier: tierName, Reason: "forced"}
	} else if hasMedia {
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

		lastTier, msgCount := cs.Sessions.Context(apiChatID)
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

	// Agent dispatch: delegate to multi-agent coordinator.
	if tierName == "agent" && cs.Orchestrator != nil {
		var orchSysPrompts []string
		orchSysArgs := memory.CollectPrompts(cs.ContextDir)
		for i := 0; i < len(orchSysArgs)-1; i += 2 {
			if orchSysArgs[i] == "--append-system-prompt" {
				orchSysPrompts = append(orchSysPrompts, orchSysArgs[i+1])
			}
		}
		if recallBlock := recallMemories(cs.Recaller, req.Message); recallBlock != "" {
			orchSysPrompts = append(orchSysPrompts, recallBlock)
		}
		if cs.SkillStore != nil {
			if catalog := skills.BuildCatalog(cs.SkillStore); catalog != "" {
				orchSysPrompts = append(orchSysPrompts, catalog)
			}
		}

		onProgress := func(phase, detail string) {
			switch phase {
			case "thinking":
				onEvent(ChatEvent{Type: "thinking", Data: map[string]string{}})
			case "agent":
				onEvent(ChatEvent{Type: "tool_use", Data: map[string]string{"name": "agent:" + detail}})
			}
		}

		orchResult, orchMeta, orchErr := cs.Orchestrator.Run(ctx, prompt, orchSysPrompts, agents.RunConfig{
			Model:                tp.Model,
			Effort:               tp.Effort,
			MaxTurns:             tp.MaxTurns,
			OrchestratorMaxTurns: tp.OrchestratorMaxTurns,
			MaxIterations:        tp.MaxIterations,
			TimeoutMin:           tp.TimeoutMin,
			Tools:                tp.Tools,
		}, onProgress)
		if orchErr != nil {
			return fmt.Errorf("agent: %w", orchErr)
		}

		assistantMsg := ChatMessage{
			ID:        NewMessageID(),
			Role:      "assistant",
			Text:      orchResult,
			Timestamp: time.Now(),
			Model:     "agent",
			Tier:      "agent",
			CostUSD:   orchMeta.TotalCost,
		}
		cs.ChatStore.Append(assistantMsg)

		onEvent(ChatEvent{Type: "text", Data: map[string]string{"text": orchResult}})
		onEvent(ChatEvent{Type: "done", Data: ChatDoneData{
			MsgID:   assistantMsg.ID,
			Model:   "agent",
			CostUSD: orchMeta.TotalCost,
			Tier:    "agent",
		}})

		cs.EventLog.Log("agent_out", map[string]any{
			"iterations":  orchMeta.Iterations,
			"total_cost":  orchMeta.TotalCost,
			"agent_calls": len(orchMeta.AgentCalls),
			"task_id":     orchMeta.ID,
			"source":      "api",
		})
		return nil
	}

	// Build system prompts.
	systemPrompts := memory.CollectPrompts(cs.ContextDir)
	// Convert --append-system-prompt flags to flat strings.
	var sysPromptTexts []string
	for i := 0; i < len(systemPrompts)-1; i += 2 {
		if systemPrompts[i] == "--append-system-prompt" {
			sysPromptTexts = append(sysPromptTexts, systemPrompts[i+1])
		}
	}
	// Auto-inject relevant memories from long-term store.
	if recallBlock := recallMemories(cs.Recaller, req.Message); recallBlock != "" {
		sysPromptTexts = append(sysPromptTexts, recallBlock)
	}
	// Inject skill catalog so the model knows available skills.
	if cs.SkillStore != nil {
		if catalog := skills.BuildCatalog(cs.SkillStore); catalog != "" {
			sysPromptTexts = append(sysPromptTexts, catalog)
		}
		// Auto-inject skills whose triggers match the user message.
		if matched := skills.MatchTriggers(cs.SkillStore, req.Message); len(matched) > 0 {
			names := make([]string, len(matched))
			for i, sk := range matched {
				names[i] = sk.Name
			}
			log.Printf("[chat-api] skills: auto-injected %v", names)
			sysPromptTexts = append(sysPromptTexts, skills.BuildInjection(matched))
		}
	}
	sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(reactionSystemPromptTmpl, mood.AllowedReactionList()))

	// Select provider based on tier backend.
	prov := cs.Provider
	isAPITier := tp.Backend == "openrouter"
	if cs.Registry != nil {
		prov = cs.Registry.ForBackend(tp.Backend)
	}

	// Invoke via selected Provider.
	resumeID := cs.Sessions.Get(apiChatID)
	params := provider.Params{
		Model:         tp.Model,
		Tools:         tp.Tools,
		WriteCapable:  tp.WriteCapable,
		Effort:        tp.Effort,
		SystemPrompts: sysPromptTexts,
		ResumeID:      resumeID,
		DataDir:       cs.DataDir,
	}
	if isAPITier {
		params.SessionKey = fmt.Sprintf("cc:%d", apiChatID)
		params.ResumeID = "" // API tiers use history, not --resume
	}

	var progressFn provider.OnProgress
	progressFn = func(event provider.StreamEvent) {
		switch event.Type {
		case "thinking":
			if event.Text != "" {
				onEvent(ChatEvent{Type: "thinking", Data: map[string]string{"text": event.Text}})
			} else {
				onEvent(ChatEvent{Type: "thinking", Data: map[string]string{}})
			}
		case "tool_use":
			onEvent(ChatEvent{Type: "tool_use", Data: map[string]string{"name": event.Detail}})
		case "text_delta":
			onEvent(ChatEvent{Type: "text_delta", Data: map[string]string{"text": event.Text}})
		}
	}

	result, err := prov.Invoke(ctx, prompt, params, progressFn)

	// Retry without resume if session not found (CLI only).
	if err != nil && resumeID != "" && !isAPITier && strings.Contains(err.Error(), "No conversation found") {
		log.Printf("[chat-api] session %s expired, starting fresh", resumeID)
		cs.Sessions.Archive(apiChatID)
		params.ResumeID = ""
		result, err = prov.Invoke(ctx, prompt, params, nil)
	}

	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}

	// Update session.
	if result.SessionID != "" {
		cs.Sessions.SetWithContext(apiChatID, result.SessionID, tierName)
	} else if isAPITier {
		cs.Sessions.TouchContext(apiChatID, tierName)
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
	state := mood.GetCurrentState(cs.ContextDir)
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
	mood.UpdateLiveFeedback(cs.ContextDir, cs.DataDir)

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

// NewSession archives the current API chat session and optionally triggers onboarding.
func (cs *ChatService) NewSession(onboard bool) string {
	old := cs.Sessions.Archive(apiChatID)
	if cs.APIHistory != nil {
		cs.APIHistory.Clear(fmt.Sprintf("cc:%d", apiChatID))
	}
	if onboard {
		memory.SetOnboarding(cs.ContextDir)
	}
	return old
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
			// For CLI backend, resolve short names; for openrouter, use as-is.
			if t.Backend != "openrouter" && cs.ResolveModel != nil {
				model = cs.ResolveModel(t.Model)
			}
			return tierParams{
				Model:                model,
				Tools:                t.Tools,
				Effort:               t.Effort,
				Backend:              t.Backend,
				WriteCapable:         t.WriteCapable,
				MaxTurns:             t.MaxTurns,
				OrchestratorMaxTurns: t.OrchestratorMaxTurns,
				MaxIterations:        t.MaxIterations,
				TimeoutMin:           t.TimeoutMin,
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
	Model                string
	Tools                []string
	Effort               string
	Backend              string
	WriteCapable         bool
	MaxTurns             int
	OrchestratorMaxTurns int
	MaxIterations        int
	TimeoutMin           int
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

	params := provider.Params{
		Model:    tp.Model,
		ResumeID: resumeID,
		DataDir:  cs.DataDir,
	}

	result, err := cs.Provider.Invoke(context.Background(), prompt, params, nil)
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
		frames, err := media.ExtractFrames(tmpFile.Name(), 16)
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
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"application/pdf": ".pdf",
		"video/mp4":       ".mp4",
		"video/quicktime": ".mov",
		"video/webm":      ".webm",
		"audio/ogg":       ".ogg",
		"audio/mpeg":      ".mp3",
		"audio/wav":       ".wav",
	}
	if ext, ok := mimeToExt[mimeType]; ok {
		return ext
	}
	if ext := filepath.Ext(fileName); ext != "" {
		return ext
	}
	return ""
}

// recallMemories searches long-term memory for relevant context.
// Returns a formatted system prompt block, or "" if nothing relevant.
const recallDistanceThreshold = 1.2
const recallLimit = 3

// StartJob launches Ask in a background goroutine and returns the job for streaming.
// If a job is already running, returns it for reconnection.
func (cs *ChatService) StartJob(req ChatRequest) *chatJob {
	cs.jobMu.Lock()
	if j := cs.activeJob; j != nil && !j.isDone() {
		cs.jobMu.Unlock()
		return j
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := newChatJob(cancel)
	cs.activeJob = job
	cs.jobMu.Unlock()

	go func() {
		err := cs.Ask(ctx, req, func(evt ChatEvent) {
			job.push(evt)
		})
		job.finish(err)
	}()

	return job
}

// ActiveJob returns the current in-flight job, or nil if none.
func (cs *ChatService) ActiveJob() *chatJob {
	cs.jobMu.Lock()
	defer cs.jobMu.Unlock()
	if cs.activeJob != nil && !cs.activeJob.isDone() {
		return cs.activeJob
	}
	return nil
}

// GetJob returns a job by ID (even if completed) for reconnection.
func (cs *ChatService) GetJob(id string) *chatJob {
	cs.jobMu.Lock()
	defer cs.jobMu.Unlock()
	if cs.activeJob != nil && cs.activeJob.ID == id {
		return cs.activeJob
	}
	return nil
}

func recallMemories(recaller MemoryRecaller, message string) string {
	if recaller == nil || len(message) < 5 {
		return ""
	}
	q := message
	if len(q) > 60 {
		q = q[:60] + "..."
	}
	results, err := recaller.Search(message, recallLimit)
	if err != nil {
		log.Printf("[chat-api] recall search error for %q: %v", q, err)
		return ""
	}
	if len(results) == 0 {
		log.Printf("[chat-api] recall: no results for %q", q)
		return ""
	}
	var relevant []MemoryResult
	for _, r := range results {
		if r.Distance < recallDistanceThreshold {
			relevant = append(relevant, r)
		}
	}
	if len(relevant) == 0 {
		log.Printf("[chat-api] recall: %d results for %q but all filtered (dist>=%.1f)", len(results), q, recallDistanceThreshold)
		return ""
	}
	var sb strings.Builder
	sb.WriteString("=== [auto-recall] ===\nRelevant memories about the user (auto-retrieved):\n")
	for _, r := range relevant {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", r.Type, r.Text))
	}
	log.Printf("[chat-api] recall: injected %d memories for %q", len(relevant), q)
	return sb.String()
}
