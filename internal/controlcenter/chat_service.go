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

	agents "github.com/alamparelli/alf/internal/runtime/agents"
	"github.com/alamparelli/alf/internal/comms"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/mood"
	provider "github.com/alamparelli/alf/internal/ai/provider"
	chatsession "github.com/alamparelli/alf/internal/session"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
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

// apiChatID is the default session key for API clients. Negative to avoid
// collision with Telegram IDs (always positive for users/groups).
const apiChatID int64 = -1

// convSessionID returns a unique session key for a given conversation tab.
// Each conv_id gets its own session so tabs don't share Claude CLI resume state.
// Empty conv_id falls back to the default apiChatID (-1).
func convSessionID(convID string) int64 {
	if convID == "" {
		return apiChatID
	}
	// FNV-1a hash of conv_id, negated to stay in negative range (avoid TG collision).
	var h int64 = -2166136261
	for i := 0; i < len(convID); i++ {
		h ^= int64(convID[i])
		h *= 16777619
	}
	if h >= 0 {
		h = -h - 2 // ensure negative, avoid -1 (apiChatID) and 0
	}
	return h
}

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
	Memory       memory.Store           // unified store (replaces ChatDB + ConvStore)
	Transcriber  *voice.Transcriber     // may be nil
	Classify     ClassifyFunc           // injected router
	ResolveModel ResolveModelFunc       // injected model resolver
	Provider     provider.Provider      // injected Claude provider (default)
	Registry     *provider.Registry     // may be nil - multi-backend dispatch
	Recaller     MemoryRecaller         // may be nil - auto-injects relevant memories
	MemStore     MemoryStorer           // may be nil - stores reaction-based learnings
	SkillStore   skills.Store           // may be nil - injects skill catalog into system prompts
	Orchestrator  *agents.Orchestrator             // may be nil - multi-agent orchestrator
	ToolRegistry  *tooling.Registry                // may be nil - tool schemas for API agentic loop
	ToolExecutor    *tooling.Executor              // may be nil - tool subprocess runner
	BackendConfigs  func() map[string]BackendConfig // may be nil - backend pricing lookup
	Engine          *comms.ChatEngine              // may be nil - unified engine (Step 5+)
	ccAdapter       *ccAdapter                     // bridges engine events to per-call callbacks

	// Background job tracking - one active job per conversation.
	activeJobs   map[string]*chatJob // conv_id → job
	jobMu        sync.Mutex
	lastChatConv string // last conv_id used by the CC chat frontend

	// askOverride, if non-nil, replaces askViaEngine. Test-only seam for
	// exercising StartJob concurrency without standing up a full ChatEngine.
	askOverride func(ctx context.Context, req ChatRequest, onEvent func(ChatEvent)) error

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
	ConvID   string   `json:"conv_id,omitempty"` // conversation tab ID (empty = default)

	// preInsertedUserMsgID is set by StartJob after persisting the user
	// message synchronously (#310). Flows to engine.InMessage so the
	// pipeline skips re-insertion. Not JSON-serialized.
	preInsertedUserMsgID string
}

// ChatDoneData is sent with the "done" event.
type ChatDoneData struct {
	MsgID      string   `json:"msg_id"`
	SessionID  string   `json:"session_id"`
	Model      string   `json:"model"`
	CostUSD    float64  `json:"cost_usd"`
	Tier       string   `json:"tier"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	Skills     []string `json:"skills,omitempty"`
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

// reactionSystemPromptTmpl references the centralized prompt in memory/reaction.md.

// ChatServiceOpts groups optional dependencies for ChatService.
// All fields are optional and nil-safe — ChatService guards access to each.
type ChatServiceOpts struct {
	Registry       *provider.Registry
	SkillStore     skills.Store
	Orchestrator   *agents.Orchestrator
	ToolRegistry   *tooling.Registry
	ToolExecutor   *tooling.Executor
	Recaller       MemoryRecaller
	MemStore       MemoryStorer
	BackendConfigs func() map[string]BackendConfig
}

// NewChatService creates a new ChatService.
func NewChatService(dataDir, configDir, contextDir string, tierStore TierStore, sessions *chatsession.Store, eventLog *eventlog.Logger, mem memory.Store, transcriber *voice.Transcriber, classify ClassifyFunc, resolveModel ResolveModelFunc, prov provider.Provider) *ChatService {
	cs := &ChatService{
		DataDir:      dataDir,
		ConfigDir:    configDir,
		ContextDir:   contextDir,
		TierStore:    tierStore,
		Sessions:     sessions,
		EventLog:     eventLog,
		Memory:       mem,
		Transcriber:  transcriber,
		Classify:     classify,
		ResolveModel: resolveModel,
		Provider:     prov,
		uploads:      make(map[string]*UploadEntry),
		activeJobs:   make(map[string]*chatJob),
	}
	// Start upload cleanup goroutine.
	go cs.cleanupUploads()
	return cs
}

// Init configures optional dependencies. Must be called before the first Ask().
// Replaces post-construction field assignments — all deps validated in one place.
func (cs *ChatService) Init(opts ChatServiceOpts) {
	cs.Registry = opts.Registry
	cs.SkillStore = opts.SkillStore
	cs.Orchestrator = opts.Orchestrator
	cs.ToolRegistry = opts.ToolRegistry
	cs.ToolExecutor = opts.ToolExecutor
	cs.Recaller = opts.Recaller
	cs.MemStore = opts.MemStore
	cs.BackendConfigs = opts.BackendConfigs
}

// SetEngine installs the unified comms engine and registers the CC adapter.
func (cs *ChatService) SetEngine(engine *comms.ChatEngine) {
	cs.Engine = engine
	cs.ccAdapter = newCCAdapter()
	cs.ccAdapter.Memory = cs.Memory
	engine.RegisterAdapter(cs.ccAdapter)
}

// SetEventBroker wires the event broker into the CC adapter for standalone notifications.
func (cs *ChatService) SetEventBroker(broker *EventBroker) {
	if cs.ccAdapter != nil {
		cs.ccAdapter.EventBroker = broker
	}
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
// No global mutex: concurrency is bounded per-conversation by StartJob's
// activeJobs check. A hang in one conv must not stall other convs (issue #312).
func (cs *ChatService) Ask(ctx context.Context, req ChatRequest, onEvent func(ChatEvent)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cs.askOverride != nil {
		return cs.askOverride(ctx, req, onEvent)
	}
	return cs.askViaEngine(ctx, req, onEvent)
}

// askViaEngine delegates message processing to the unified comms engine.
// Engine handles persistence to ChatDB. CC-specific: SSE bridge, force commands, mood reactions.
func (cs *ChatService) askViaEngine(ctx context.Context, req ChatRequest, onEvent func(ChatEvent)) error {
	if cs.Engine == nil {
		return fmt.Errorf("chat engine not initialized — call SetEngine() before Ask()")
	}

	sessID := convSessionID(req.ConvID)
	channelID := comms.ChannelID("cc:" + req.ConvID)
	if req.ConvID == "" {
		channelID = "cc:default"
	}

	// 0a. /resume: rewrite to continuation prompt and fall through to Process().
	if newText, isResume, errMsg := cs.Engine.HandleResume(channelID, req.Message); isResume {
		req.Message = newText
		log.Printf("[cc] /resume → continuing session")
	} else if errMsg != "" {
		onEvent(ChatEvent{Type: "system", Data: map[string]string{"text": errMsg}})
		onEvent(ChatEvent{Type: "done", Data: ChatDoneData{}})
		return nil
	}

	// 0b. Built-in command handling via comms engine (/new, /skills, etc.).
	if strings.HasPrefix(req.Message, "/") {
		if response, handled := cs.Engine.HandleCommand(channelID, req.Message); handled {
			// Persist the slash command as a user message so it stays visible in chat.
			if cs.Memory != nil && req.ConvID != "" {
				_ = cs.Memory.EnsureConv(ctx, memory.ConvID(req.ConvID), "", "cc")
				_, _ = cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), memory.Message{
					Role: "user", Channel: "cc",
					Content: req.Message,
					Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: req.Message}},
				})
			}
			if response != "" {
				onEvent(ChatEvent{Type: "system", Data: map[string]string{"text": response}})
				if cs.Memory != nil && req.ConvID != "" {
					_, _ = cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), memory.Message{
						Role: "system", Channel: "cc",
						Content: response,
						Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: response}},
					})
				}
			}
			onEvent(ChatEvent{Type: "done", Data: ChatDoneData{}})
			return nil
		}
	}

	// 1. Force command detection: /<tier> or /<skill> (CC-specific UI feedback).
	// Runs even when req.Model is already set (e.g. the CC tier selector
	// pre-fills it) — an explicit /tier prefix is a stronger user intent
	// than the dropdown and must win. When a force command matches, req.Model
	// is rewritten below to the forced tier.
	if strings.HasPrefix(req.Message, "/") {
		parts := strings.SplitN(req.Message, " ", 2)
		cmdName := strings.TrimPrefix(parts[0], "/")
		tierMatched := false

		for _, t := range cs.TierStore.Current().Tiers {
			if t.Enabled && t.ForceCommand && (t.Name == cmdName || SanitizeTierCommand(t.Name) == cmdName) {
				tierMatched = true
				cs.Sessions.SetForcedTier(sessID, t.Name)
				sysText := fmt.Sprintf("Session locked to **%s**. Use /new to reset.", t.Name)
				onEvent(ChatEvent{Type: "system", Data: map[string]string{"text": sysText}})
				if cs.Memory != nil && req.ConvID != "" {
					_, _ = cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), memory.Message{
						Role: "system", Channel: "cc",
						Content: sysText,
						Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: sysText}},
					})
				}
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					// Persist the force command as a user message.
					if cs.Memory != nil && req.ConvID != "" {
						_, _ = cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), memory.Message{
							Role: "user", Channel: "cc",
							Content: req.Message,
							Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: req.Message}},
						})
					}
					onEvent(ChatEvent{Type: "done", Data: ChatDoneData{Model: t.Name, Tier: t.Name}})
					return nil
				}
				req.Model = t.Name
				req.Message = strings.TrimSpace(parts[1])
				break
			}
		}

		if cs.Engine.SkillStore != nil && !tierMatched {
			if sk, ok := cs.Engine.SkillStore.Get(cmdName); ok {
				sessionKey := channelID.SessionKey()
				cs.Engine.Sessions.AddSkills(sessionKey, []string{sk.Name})
				desc := sk.Description
				if desc != "" {
					desc = " — " + desc
				}
				skillText := fmt.Sprintf("Skill **%s** activated%s", sk.Name, desc)
				onEvent(ChatEvent{Type: "system", Data: map[string]string{"text": skillText}})
				if cs.Memory != nil && req.ConvID != "" {
					_, _ = cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), memory.Message{
						Role: "system", Channel: "cc",
						Content: skillText,
						Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: skillText}},
					})
				}
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					// Persist the skill command as a user message.
					if cs.Memory != nil && req.ConvID != "" {
						_, _ = cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), memory.Message{
							Role: "user", Channel: "cc",
							Content: req.Message,
							Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: req.Message}},
						})
					}
					onEvent(ChatEvent{Type: "done", Data: ChatDoneData{}})
					return nil
				}
				req.Message = strings.TrimSpace(parts[1])
			}
		}
	}

	// Check persistent tier override.
	if req.Model == "" {
		if ft := cs.Sessions.GetForcedTier(sessID); ft != "" {
			req.Model = ft
		}
	}

	// 2. Build prompt (CC-specific: upload registry, media paths, reply context).
	prompt := cs.buildPrompt(req)
	if prompt == "" {
		return fmt.Errorf("empty message")
	}

	// Build router text with reply context hint.
	routerMsg := req.Message
	if req.ReplyTo != "" && cs.Memory != nil && req.ConvID != "" {
		if orig, _ := cs.Memory.GetMessage(ctx, memory.ConvID(req.ConvID), memory.MsgID(req.ReplyTo)); orig != nil {
			quoted := orig.Content
			if len(quoted) > 100 {
				quoted = quoted[:100] + "..."
			}
			routerMsg = fmt.Sprintf("[Replying to: \"%s\"]\n%s", quoted, req.Message)
		}
	}

	// 3. Build InMessage for engine. If StartJob pre-persisted the user
	// message, pass the ID so the engine skips re-insertion (#310).
	msg := comms.InMessage{
		ChannelID:            channelID,
		Text:                 prompt,
		RawText:              req.Message,
		RouterText:           routerMsg,
		IsReply:              req.ReplyTo != "",
		ForcedTier:           req.Model,
		ConvID:               req.ConvID,
		Source:               "cc",
		ReplyToMsgID:         req.ReplyTo,
		PreInsertedUserMsgID: req.preInsertedUserMsgID,
	}
	if req.ReplyTo != "" && cs.Memory != nil && req.ConvID != "" {
		if orig, _ := cs.Memory.GetMessage(ctx, memory.ConvID(req.ConvID), memory.MsgID(req.ReplyTo)); orig != nil {
			msg.ReplyTo = orig.Content
		}
	}

	// Populate Media field for API providers (vision support).
	for _, mid := range req.MediaIDs {
		entry := cs.GetUpload(mid)
		if entry == nil {
			continue
		}
		msg.Media = append(msg.Media, comms.MediaEntry{
			Type:        entry.MediaType,
			FileName:    entry.FileName,
			MimeType:    entry.MimeType,
			TempPath:    entry.TempPath,
			FramePaths:  entry.FramePaths,
			Transcript:  entry.Transcript,
			TextContent: entry.TextContent,
		})
	}

	// 4. Set up event bridge (suppress engine's "done", we emit our own).
	cs.ccAdapter.setCallback(onEvent)
	defer cs.ccAdapter.setCallback(nil)

	// 5. Call engine.Process().
	start := time.Now()
	result, err := cs.Engine.Process(ctx, msg)
	duration := time.Since(start)

	if err != nil {
		return err
	}

	// 5b. Persist media refs for the user message.
	// TODO(#336): memory.Store does not expose a post-hoc media-attach API —
	// media is attached at AppendMessage time. Folding it into the user
	// message requires threading req.MediaIDs through persistUserMessage +
	// InMessage.Media; handled at those call sites. The loop below is now
	// a no-op kept for the issue trail.
	_ = result

	// 6. Spontaneous mood reaction (CC-specific).
	state := mood.GetCurrentState(cs.ContextDir)
	if mood.ShouldReact(state) {
		spontaneous := mood.ChooseSpontaneous(state)
		if spontaneous != "" {
			onEvent(ChatEvent{Type: "reaction", Data: map[string]string{"emoji": spontaneous}})
		}
	}

	// 7. Emit "done" with ChatDoneData (CC-specific rich format).
	// Assistant message was already persisted by Engine via ChatDB.
	doneData := ChatDoneData{
		MsgID:      result.AssistantMsgID,
		SessionID:  result.SessionID,
		Model:      result.Model,
		CostUSD:    result.CostUSD,
		Tier:       result.Tier,
		DurationMs: duration.Milliseconds(),
	}
	if sk := result.Skills; len(sk) > 0 {
		doneData.Skills = sk
	}
	onEvent(ChatEvent{Type: "done", Data: doneData})

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

	// Resolve the owning convID and record the reaction in the memory store.
	ctx := context.Background()
	convID := cs.findConvForMsg(ctx, req.MsgID)
	if cs.Memory != nil && convID != "" {
		_, _ = cs.Memory.AddReaction(ctx, memory.ConvID(convID), memory.MsgID(req.MsgID), memory.Reaction{Emoji: emoji, Source: "user"})
	}

	result := &ReactResult{OK: true}

	_, state := mood.GetTodayScore(cs.DataDir)
	if mood.ShouldReact(state) {
		mirror := mood.ChooseMirror(emoji, state)
		if mirror != "" {
			result.Mirror = mirror
			if cs.Memory != nil && convID != "" {
				_, _ = cs.Memory.AddReaction(ctx, memory.ConvID(convID), memory.MsgID(req.MsgID), memory.Reaction{Emoji: mirror, Source: "alf"})
			}
		}
	}

	// Extract preference learning via comms engine.
	if cs.Engine != nil {
		go cs.Engine.ExtractReactionLearning(emoji, comms.ChannelID("cc:0"))
	}

	// Async negative follow-up.
	if mood.IsNegative(emoji) {
		go cs.negativeFollowUp(emoji, req.MsgID)
	}

	return result, nil
}

// NewSession archives the current API chat session and optionally triggers onboarding.
// Delegates to the unified comms engine which handles session archival, skill clearing,
// onboarding state, event logging, and memory extraction hooks.
// Returns (oldSessionID, newConvID).
func (cs *ChatService) NewSession(onboard bool) (string, string) {
	var old string
	if cs.Engine != nil {
		old = cs.Engine.NewSession(comms.ChannelID("cc:"+fmt.Sprint(apiChatID)), onboard)
	} else {
		// Legacy fallback when engine is not wired.
		old = cs.Sessions.Archive(apiChatID)
		if cs.Memory != nil {
			ctx := context.Background()
			newID := memory.NewConvID()
			_ = cs.Memory.SetPref(ctx, "active_conv:"+memory.ChannelCC, string(newID))
		}
		if onboard {
			memory.SetOnboarding(cs.ContextDir)
		} else {
			memory.ClearOnboarding(cs.ContextDir)
		}
	}
	var newConvID string
	if cs.Memory != nil {
		ctx := context.Background()
		v, _ := cs.Memory.GetPref(ctx, "active_conv:"+memory.ChannelCC)
		if s, ok := v.(string); ok {
			newConvID = s
		}
	}
	if newConvID == "" {
		newConvID = string(memory.NewConvID())
	}
	return old, newConvID
}

// ActiveSkills returns the names of skills currently active in the CC session.
func (cs *ChatService) ActiveSkills() []string {
	return cs.Sessions.GetSkills(apiChatID)
}

// ActiveSkillsForConv returns active skills for a specific conversation.
func (cs *ChatService) ActiveSkillsForConv(convID string) []string {
	return cs.Sessions.GetSkills(convSessionID(convID))
}

// RemoveActiveSkill removes a single skill from the CC session.
func (cs *ChatService) RemoveActiveSkill(name string) {
	cs.Sessions.RemoveSkill(apiChatID, name)
}

// RemoveActiveSkillForConv removes a single skill for a specific conversation.
func (cs *ChatService) RemoveActiveSkillForConv(convID, name string) {
	cs.Sessions.RemoveSkill(convSessionID(convID), name)
}

// ClearActiveSkills removes all active skills from the CC session.
func (cs *ChatService) ClearActiveSkills() {
	cs.Sessions.ClearSkills(apiChatID)
}

// ClearActiveSkillsForConv clears all active skills for a specific conversation.
func (cs *ChatService) ClearActiveSkillsForConv(convID string) {
	cs.Sessions.ClearSkills(convSessionID(convID))
}

// CurrentConvID returns the active conversation ID for the CC chat.
//
// Priority:
//  1. in-memory lastChatConv (within this daemon run)
//  2. persisted kv_meta.active_conv_id — validated against the non-archived
//     CC conversations list so a stale pointer (e.g. user archived the conv)
//     doesn't leak through
//  3. most recent non-archived CC conversation from ChatDB
//  4. "" — frontend will create a fresh conv
//
// This no longer falls back to ConvStore.ConvID. ConvStore IDs are an
// internal tracking namespace unrelated to the UI's tab IDs, and returning
// one caused #318 — after a restart with no kv_meta, the server returned
// a freshly-minted "conv-%x" that the UI couldn't match against its
// conversation list, rotating the user to a brand-new tab.
func (cs *ChatService) CurrentConvID() string {
	if cs.lastChatConv != "" {
		return cs.lastChatConv
	}
	if cs.Memory == nil {
		return ""
	}
	ctx := context.Background()
	convs, _ := cs.Memory.ListConvs(ctx, memory.ConvFilter{Channel: "cc"})
	if v, _ := cs.Memory.GetPref(ctx, "active_conv_id"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			for _, c := range convs {
				if string(c.ID) == s {
					return s
				}
			}
		}
	}
	if len(convs) > 0 {
		return string(convs[len(convs)-1].ID)
	}
	return ""
}

// SetActiveConvID persists the active conversation and updates in-memory state.
// Also ensures the conv row exists in Memory so CurrentConvID's validation
// (introduced for #318) can resolve it on the next call.
func (cs *ChatService) SetActiveConvID(convID string) {
	cs.lastChatConv = convID
	if cs.Memory != nil {
		ctx := context.Background()
		if convID != "" {
			_ = cs.Memory.EnsureConv(ctx, memory.ConvID(convID), "", "cc")
		}
		_ = cs.Memory.SetPref(ctx, "active_conv_id", convID)
	}
}

// History returns paginated chat history, optionally filtered by conversation.
//
// The HTTP JSON shape must stay stable: the memory store uses int64 unix
// millis for CreatedAt while the historical wire contract exposed time.Time
// under the "ts" key. historyMessage is the JSON boundary type that pins
// that shape.
func (cs *ChatService) History(limit int, before time.Time, convID string) []HistoryMessage {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if cs.Memory == nil || convID == "" {
		return []HistoryMessage{}
	}
	ctx := context.Background()
	// TODO(#336): before-timestamp pagination dropped in memory migration;
	// use cursor-based Before MsgID instead. Most callers pass zero Time.
	if !before.IsZero() {
		log.Printf("[chat-service] history: 'before' timestamp pagination not supported in memory store; returning latest")
	}
	msgs, err := cs.Memory.ListMessages(ctx, memory.ConvID(convID), memory.ListOpts{Limit: limit, ApplySummary: false})
	if err != nil {
		log.Printf("[chat-service] history error: %v", err)
		return []HistoryMessage{}
	}
	out := make([]HistoryMessage, len(msgs))
	for i, m := range msgs {
		out[i] = memoryMessageToHistory(m, convID)
	}
	return out
}

// Conversations returns all known conversation summaries.
func (cs *ChatService) Conversations() []ConversationHistory {
	if cs.Memory == nil {
		return []ConversationHistory{}
	}
	ctx := context.Background()
	convs, err := cs.Memory.ListConvs(ctx, memory.ConvFilter{})
	if err != nil {
		log.Printf("[chat-service] conversations error: %v", err)
		return []ConversationHistory{}
	}
	out := make([]ConversationHistory, len(convs))
	for i, c := range convs {
		out[i] = ConversationHistory{
			ID:          string(c.ID),
			Title:       c.Title,
			Source:      c.Channel,
			CreatedAt:   time.UnixMilli(c.CreatedAt),
			UpdatedAt:   time.UnixMilli(c.UpdatedAt),
			LastMessage: time.UnixMilli(c.LastMessage),
			Archived:    c.Archived,
			MsgCount:    c.MsgCount,
		}
	}
	return out
}

// buildPrompt constructs the full prompt from a ChatRequest.
func (cs *ChatService) buildPrompt(req ChatRequest) string {
	var parts []string

	// Reply context.
	if req.ReplyTo != "" && cs.Memory != nil && req.ConvID != "" {
		if orig, _ := cs.Memory.GetMessage(context.Background(), memory.ConvID(req.ConvID), memory.MsgID(req.ReplyTo)); orig != nil {
			parts = append(parts, fmt.Sprintf("[The user is replying to this previous message:\n---\n%s\n---\n]", orig.Content))
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
			parts = append(parts, fmt.Sprintf("[PHOTO - use Read tool to view: %s]", entry.TempPath))
		case media.IsVideoContent(entry.MimeType, entry.FileName):
			if len(entry.FramePaths) > 0 {
				parts = append(parts, fmt.Sprintf("[VIDEO \"%s\" - contact sheet with key frames. Use Read tool to view: %s]", entry.FileName, strings.Join(entry.FramePaths, ", ")))
			} else {
				parts = append(parts, fmt.Sprintf("[VIDEO \"%s\" - use Read tool to view: %s]", entry.FileName, entry.TempPath))
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
				parts = append(parts, fmt.Sprintf("[FILE: %s - use Read tool to view: %s]", entry.FileName, entry.TempPath))
			}
		default:
			parts = append(parts, fmt.Sprintf("[FILE: %s - use Read tool to view: %s]", entry.FileName, entry.TempPath))
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

// firstFallbackTier returns the first enabled non-instant tier, or the first enabled tier.
func (cs *ChatService) firstFallbackTier() string {
	tiers := cs.TierStore.Current()
	if tiers.DefaultFallback != "" {
		return tiers.DefaultFallback
	}
	for _, t := range tiers.Tiers {
		if t.Enabled {
			return t.Name
		}
	}
	if len(tiers.Tiers) > 0 {
		return tiers.Tiers[0].Name
	}
	return ""
}

// onboardingTier returns a capable tier for onboarding (priority >= 2, i.e. sonnet-level).
// Falls back to firstFallbackTier if nothing better is available.
func (cs *ChatService) onboardingTier() string {
	tiers := cs.TierStore.Current()
	// Pick the second-priority enabled tier (skip the cheapest).
	type candidate struct {
		name     string
		priority int
	}
	var candidates []candidate
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Name != "agent" {
			candidates = append(candidates, candidate{t.Name, t.Priority})
		}
	}
	if len(candidates) >= 2 {
		// Sort by priority ascending, pick second.
		best := candidates[0]
		second := candidates[1]
		if second.priority < best.priority {
			best, second = second, best
		}
		for _, c := range candidates[2:] {
			if c.priority < best.priority {
				second = best
				best = c
			} else if c.priority < second.priority {
				second = c
			}
		}
		return second.name
	}
	return cs.firstFallbackTier()
}

// resolveTierParams looks up tier config and returns CLI parameters.
func (cs *ChatService) resolveTierParams(tierName string) tierParams {
	tiers := cs.TierStore.Current()
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			model := t.Model
			backend := t.Backend
			// Auto-detect API backend from model name (e.g. "x-ai/grok-4" → openrouter).
			if (backend == "" || backend == "cli") && strings.Contains(model, "/") {
				if cs.Registry != nil {
					// Pick first registered API backend that might serve this model.
					names := cs.Registry.BackendNames()
					if len(names) > 0 {
						backend = names[0]
						log.Printf("[chat] tier %q: auto-detected backend=%s for model=%s", tierName, backend, model)
					}
				}
			}
			// For CLI backend, resolve short names; for API backends, use model string as-is.
			if (backend == "" || backend == "cli") && cs.ResolveModel != nil {
				model = cs.ResolveModel(t.Model)
			}
			// Resolve tool wildcards into concrete tool names.
			tools := t.Tools
			if len(tools) == 1 && tools[0] == "*" {
				tools = tooling.ResolveWildcard(cs.DataDir, cs.ToolRegistry)
				if len(tools) > 0 {
					log.Printf("[chat] tier %q: wildcard resolved to %d tools", tierName, len(tools))
				}
			} else if len(tools) == 1 && tools[0] == "*native" {
				// Only native Go tools (bash, read_file, grep, glob, write_file).
				if cs.ToolRegistry != nil {
					tools = cs.ToolRegistry.NativeToolNames()
				} else {
					tools = nil
				}
				if len(tools) > 0 {
					log.Printf("[chat] tier %q: native wildcard resolved to %d tools", tierName, len(tools))
				}
			}
			return tierParams{
				Model:                model,
				Tools:                tools,
				Effort:               t.Effort,
				Backend:              backend,
				WriteCapable:         t.WriteCapable,
				MaxTurns:             t.MaxTurns,
				OrchestratorMaxTurns: t.OrchestratorMaxTurns,
				MaxIterations:        t.MaxIterations,
				TimeoutMin:           t.TimeoutMin,
				SystemPrompt:         t.SystemPrompt,
				ContextWeight:        t.EffectiveContextWeight(),
			}
		}
	}
	// Tier not found — resolve from the user's configured fallback rather
	// than hardcoding a Claude model (users may run any backend).
	return tierParams{Model: DefaultFallbackModel(cs.TierStore.Current())}
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
	SystemPrompt         string
	ContextWeight        string
}

// EffectiveContextWeight returns the context weight, defaulting to "full".
func (tp tierParams) EffectiveContextWeight() string {
	switch tp.ContextWeight {
	case "light", "standard", "full":
		return tp.ContextWeight
	default:
		return "full"
	}
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

	langNote := "IMPORTANT: Reply in the same language the user has been using in this conversation."
	var prompt string
	if mood.IsStrongNegative(emoji) {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (strong negative). Something is clearly wrong. Acknowledge the negative feedback briefly, identify what likely went wrong in your previous response, and ask a short direct question to understand what they expected. Keep it to 2-3 sentences max. Don't be defensive. %s", emoji, langNote)
	} else {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (mild negative). Briefly acknowledge the feedback and ask a short question to understand what could be improved. One or two sentences max. Stay casual. %s", emoji, langNote)
	}

	resumeID := cs.Sessions.Get(apiChatID)
	var tp tierParams
	if fallback := cs.firstFallbackTier(); fallback != "" {
		tp = cs.resolveTierParams(fallback)
	}
	if tp.Model == "" {
		tp.Model = DefaultFallbackModel(cs.TierStore.Current())
	}
	if tp.Model == "" {
		log.Printf("[chat-api] no fallback model configured, skipping negative follow-up")
		return
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
	if cs.Memory != nil {
		ctx := context.Background()
		_ = cs.Memory.EnsureConv(ctx, "_followup", "", "cc")
		_, _ = cs.Memory.AppendMessage(ctx, "_followup", memory.Message{
			Role:    "assistant",
			Channel: "cc",
			Content: cleanText,
			Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: cleanText}},
			Model:   result.Model,
			Tier:    "follow-up",
		})
	}

	cs.EventLog.Log("negative_followup", map[string]any{
		"emoji":  emoji,
		"model":  result.Model,
		"source": "api",
	})
}

// cleanupUploads periodically removes expired upload entries from the registry.
// Files on disk are kept for persistent access; only the in-memory registry is pruned.
func (cs *ChatService) cleanupUploads() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cs.uploadsMu.Lock()
		now := time.Now()
		for id, entry := range cs.uploads {
			if now.Sub(entry.CreatedAt) > 24*time.Hour {
				// Clean up extracted video frames (ephemeral), keep main media file.
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

	// Save to persistent media directory (survives container restart).
	mediaDir := filepath.Join(cs.DataDir, "media")
	os.MkdirAll(mediaDir, 0o755)
	tmpFile, err := os.CreateTemp(mediaDir, "alf-media-*"+ext)
	if err != nil {
		return nil, fmt.Errorf("create media file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("write media file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("close media file: %w", err)
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
				// Delete audio after 5 minutes (keeps it for debugging).
				go func(p string) {
					time.Sleep(5 * time.Minute)
					os.Remove(p)
				}(audioPath)
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
const recallDistanceThreshold = DefaultRecallMinDist
const recallLimit = DefaultRecallTopK

// jobMaxDuration caps any single chat job. A wedged provider must not tie up
// the conversation slot (and thus the user's tab) indefinitely — this is the
// safety net against issue #312-style hangs where manual restart was needed.
const jobMaxDuration = 20 * time.Minute

// persistUserMessage inserts the user message into the memory store
// synchronously so it survives refresh even if the provider call never
// completes (#310). Returns the message ID, or "" for slash commands
// (which the engine handles and persists itself via HandleCommand).
func (cs *ChatService) persistUserMessage(req ChatRequest) string {
	if cs.Memory == nil || req.ConvID == "" {
		return ""
	}
	// Slash commands persist on their own path (system + user together).
	if strings.HasPrefix(req.Message, "/") {
		return ""
	}
	if req.Message == "" && len(req.MediaIDs) == 0 {
		return ""
	}
	ctx := context.Background()
	_ = cs.Memory.EnsureConv(ctx, memory.ConvID(req.ConvID), "", "cc")
	var mediaEntries []memory.Media
	for _, mid := range req.MediaIDs {
		entry := cs.GetUpload(mid)
		if entry == nil {
			continue
		}
		mediaEntries = append(mediaEntries, memory.Media{
			UploadID:  entry.ID,
			FileName:  entry.FileName,
			MimeType:  entry.MimeType,
			MediaType: entry.MediaType,
			FilePath:  entry.TempPath,
		})
	}
	userMsg := memory.Message{
		Role:    "user",
		Channel: "cc",
		Content: req.Message,
		Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: req.Message}},
		ReplyTo: memory.MsgID(req.ReplyTo),
		Media:   mediaEntries,
	}
	stored, err := cs.Memory.AppendMessage(ctx, memory.ConvID(req.ConvID), userMsg)
	if err != nil {
		log.Printf("[chat-service] persist user message: %v", err)
		return ""
	}
	return string(stored.ID)
}

// StartJob launches Ask in a background goroutine and returns the job for streaming.
// If a job is already running for the same conversation, returns it for reconnection.
func (cs *ChatService) StartJob(req ChatRequest) *chatJob {
	convID := req.ConvID
	cs.SetActiveConvID(convID) // persist + track for notify tool
	cs.jobMu.Lock()
	if j := cs.activeJobs[convID]; j != nil && !j.isDone() {
		cs.jobMu.Unlock()
		return j
	}

	// Persist the user message synchronously before launching the async
	// provider call (#310). Previously this happened inside engine.Process,
	// so a refresh between POST and the first provider token could lose
	// the message. Slash commands are a special case: engine.HandleCommand
	// persists them itself with system response, so skip here.
	req.preInsertedUserMsgID = cs.persistUserMessage(req)

	ctx, cancel := context.WithTimeout(context.Background(), jobMaxDuration)
	job := newChatJob(cancel)
	job.ConvID = convID
	cs.activeJobs[convID] = job
	// Prune completed jobs from other conversations.
	for k, j := range cs.activeJobs {
		if j.isDone() && k != convID {
			delete(cs.activeJobs, k)
		}
	}
	cs.jobMu.Unlock()

	log.Printf("[chat-job] start job=%s conv=%q model=%q", job.ID, convID, req.Model)

	go func() {
		start := time.Now()
		err := cs.Ask(ctx, req, func(evt ChatEvent) {
			job.push(evt)
		})
		outcome := "completed"
		switch {
		case job.wasCancelled():
			outcome = "cancelled"
		case ctx.Err() == context.DeadlineExceeded:
			outcome = "timeout"
		case err != nil:
			outcome = "error"
		}
		log.Printf("[chat-job] done  job=%s conv=%q duration=%s outcome=%s err=%v",
			job.ID, convID, time.Since(start).Round(time.Millisecond), outcome, err)
		job.finish(err)
		cancel() // release timer
	}()

	return job
}

// ActiveJob returns the current in-flight job for a conversation, or nil if none.
func (cs *ChatService) ActiveJob(convID string) *chatJob {
	cs.jobMu.Lock()
	defer cs.jobMu.Unlock()
	if j := cs.activeJobs[convID]; j != nil && !j.isDone() {
		return j
	}
	return nil
}

// GetJob returns a job by ID (even if completed) for reconnection.
func (cs *ChatService) GetJob(id string) *chatJob {
	cs.jobMu.Lock()
	defer cs.jobMu.Unlock()
	for _, j := range cs.activeJobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// ActiveJobs returns all currently running jobs across all conversations.
func (cs *ChatService) ActiveJobs() []*chatJob {
	cs.jobMu.Lock()
	defer cs.jobMu.Unlock()
	var jobs []*chatJob
	for _, j := range cs.activeJobs {
		if !j.isDone() {
			jobs = append(jobs, j)
		}
	}
	return jobs
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

// findConvForMsg walks known convs to find which one owns msgID. Used by
// React() after the migration because memory.Store addresses messages by
// (convID, msgID) — callers no longer have a flat id → message index like
// chatdb.Get provided. Most reaction flows happen shortly after send, so
// we prefer the active conv first; callers still carrying a convID should
// pass it directly to skip the scan.
func (cs *ChatService) findConvForMsg(ctx context.Context, msgID string) string {
	if cs.Memory == nil || msgID == "" {
		return ""
	}
	if cs.lastChatConv != "" {
		if m, _ := cs.Memory.GetMessage(ctx, memory.ConvID(cs.lastChatConv), memory.MsgID(msgID)); m != nil {
			return cs.lastChatConv
		}
	}
	convs, _ := cs.Memory.ListConvs(ctx, memory.ConvFilter{IncludeArchived: true})
	for _, c := range convs {
		if m, _ := cs.Memory.GetMessage(ctx, c.ID, memory.MsgID(msgID)); m != nil {
			return string(c.ID)
		}
	}
	return ""
}

// toolExecutorAdapter bridges tooling.Executor to provider.ToolExecutor.
type toolExecutorAdapter struct {
	exec   *tooling.Executor
	origin tooling.ChainOrigin // injected into context for fire-and-forget routing
}

func (a *toolExecutorAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
	if a.origin.Source != "" {
		ctx = tooling.WithChainOrigin(ctx, a.origin)
	}
	result := a.exec.Execute(ctx, tooling.CallRequest{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	return provider.ToolCallResult{
		ID:           result.ID,
		Output:       result.Output,
		IsError:      result.IsError,
		ExitCode:     result.ExitCode,
		ErrorMessage: result.ErrorMessage,
	}
}
