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
	"github.com/alamparelli/alf/internal/comms"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/mood"
	"github.com/alamparelli/alf/internal/provider"
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
	Registry     *provider.Registry   // may be nil - multi-backend dispatch
	Recaller     MemoryRecaller       // may be nil - auto-injects relevant memories
	MemStore     MemoryStorer         // may be nil - stores reaction-based learnings
	SkillStore   skills.Store         // may be nil - injects skill catalog into system prompts
	Orchestrator  *agents.Orchestrator        // may be nil - multi-agent orchestrator
	ConvStore     *conversation.Store         // may be nil - unified conversation store (Phase 1: parallel write)
	ToolRegistry  *tooling.Registry           // may be nil - tool schemas for API agentic loop
	ToolExecutor    *tooling.Executor           // may be nil - tool subprocess runner
	BackendConfigs  func() map[string]BackendConfig // may be nil - backend pricing lookup
	Engine          *comms.ChatEngine              // may be nil - unified engine (Step 5+)
	ccAdapter       *ccAdapter                     // bridges engine events to per-call callbacks
	mu              sync.Mutex                  // serialize Claude calls (single user v1)

	// Background job tracking - one active job per conversation.
	activeJobs map[string]*chatJob // conv_id → job
	jobMu      sync.Mutex

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
		activeJobs:   make(map[string]*chatJob),
	}
	// Start upload cleanup goroutine.
	go cs.cleanupUploads()
	return cs
}

// SetEngine installs the unified comms engine and registers the CC adapter.
func (cs *ChatService) SetEngine(engine *comms.ChatEngine) {
	cs.Engine = engine
	cs.ccAdapter = newCCAdapter()
	engine.RegisterAdapter(cs.ccAdapter)
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

	if cs.Engine != nil {
		return cs.askViaEngine(ctx, req, onEvent)
	}

	// Detect force command: /<tier_name> <message> bypasses routing.
	if strings.HasPrefix(req.Message, "/") && req.Model == "" {
		parts := strings.SplitN(req.Message, " ", 2)
		cmdName := strings.TrimPrefix(parts[0], "/")
		for _, t := range cs.TierStore.Current().Tiers {
			if t.Enabled && t.ForceCommand && t.Name == cmdName {
				// Persist tier override for the session.
				cs.Sessions.SetForcedTier(apiChatID, t.Name)
				onEvent(ChatEvent{Type: "system", Data: map[string]string{
					"text": fmt.Sprintf("⚡ Session locked to **%s**. Use /new to reset.", t.Name),
				}})
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					// Bare /<tier> — lock session only, no message to process.
					onEvent(ChatEvent{Type: "done", Data: ChatDoneData{Model: t.Name, Tier: t.Name}})
					return nil
				}
				req.Model = t.Name
				req.Message = strings.TrimSpace(parts[1])
				break
			}
		}
	}

	// Check for persistent tier override from a previous force command.
	if req.Model == "" {
		if ft := cs.Sessions.GetForcedTier(apiChatID); ft != "" {
			req.Model = ft
		}
	}

	// Build prompt from message + reply context + media.
	prompt := cs.buildPrompt(req)
	if prompt == "" {
		return fmt.Errorf("empty message")
	}

	// Save user message.
	userMsgID := NewMessageID()
	userMsg := ChatMessage{
		ID:        userMsgID,
		Role:      "user",
		Text:      req.Message,
		Timestamp: time.Now(),
		ConvID:    req.ConvID,
		ReplyTo:   req.ReplyTo,
		Media:     cs.resolveMediaRefs(req.MediaIDs),
	}
	cs.ChatStore.Append(userMsg)

	// Log incoming message for mem-extract (legacy path without engine).
	if cs.EventLog != nil {
		cs.EventLog.Log("message_in", map[string]any{
			"channel":   "cc",
			"text":      req.Message,
			"is_reply":  req.ReplyTo != "",
			"has_media": len(req.MediaIDs) > 0,
		})
	}

	// Parallel write to unified conversation store.
	var ccConvID string
	if cs.ConvStore != nil {
		ccConvID = cs.ConvStore.ConvID(conversation.ChannelCC)
		convUser := conversation.Message{
			ID:        userMsgID,
			ConvID:    ccConvID,
			Channel:   conversation.ChannelCC,
			Role:      "user",
			Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: req.Message}},
			Timestamp: userMsg.Timestamp,
			ReplyTo:   req.ReplyTo,
		}
		for _, mr := range userMsg.Media {
			convUser.Media = append(convUser.Media, conversation.MediaRef{
				UploadID: mr.UploadID,
				Type:     mr.Type,
				FileName: mr.FileName,
				MimeType: mr.MimeType,
				URL:      mr.URL,
			})
		}
		cs.ConvStore.Append(convUser)
	}

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

	// Register trigger-matched skills in the session.
	if cs.SkillStore != nil {
		if triggerMatched := skills.MatchTriggers(cs.SkillStore, req.Message); len(triggerMatched) > 0 {
			triggerNames := make([]string, len(triggerMatched))
			for i, sk := range triggerMatched {
				triggerNames[i] = sk.Name
			}
			log.Printf("[chat-api] skills: trigger-matched %v", triggerNames)
			cs.Sessions.AddSkills(apiChatID, triggerNames)
		}
	}

	// Skill tier override: if an active skill requires a higher tier, force it.
	if activeSkills := cs.Sessions.GetSkills(apiChatID); len(activeSkills) > 0 {
		if minTier := skills.ResolveMinTier(cs.SkillStore, activeSkills); minTier != "" {
			if routeResult.Response != "" && routeResult.Tier == "" {
				// Direct response → force to skill tier.
				routeResult = RouteResult{Tier: minTier, Reason: "skill-tier: " + minTier}
				log.Printf("[chat-api] skill tier override: direct→%s", minTier)
			} else if routeResult.Tier != "" && routeResult.Tier != minTier {
				// Check if current tier has lower priority than required tier.
				currentPri, requiredPri := -1, -1
				for _, t := range cs.TierStore.Current().Tiers {
					if t.Name == routeResult.Tier {
						currentPri = t.Priority
					}
					if t.Name == minTier {
						requiredPri = t.Priority
					}
				}
				if requiredPri >= 0 && currentPri < requiredPri {
					old := routeResult.Tier
					routeResult.Tier = minTier
					routeResult.Reason = fmt.Sprintf("skill-tier: %s→%s", old, minTier)
					log.Printf("[chat-api] skill tier override: %s→%s", old, minTier)
				}
			}
		}
	}

	// During onboarding, force a capable tier - direct responses and
	// instant tiers are too weak for the onboarding conversation.
	// During onboarding, force a capable conversational tier.
	// The lowest-priority tier (e.g. haiku) is too weak for multi-turn onboarding.
	isOnboarding := memory.OnboardingPrompt(cs.ContextDir) != ""
	if isOnboarding {
		fallback := cs.onboardingTier()
		log.Printf("[chat-api] onboarding override: %q → tier %q", routeResult.Tier, fallback)
		routeResult = RouteResult{Tier: fallback, Reason: "onboarding-override"}
	}

	// Router direct response.
	if routeResult.Response != "" && routeResult.Tier == "" {
		cs.Sessions.TouchContext(apiChatID, "router")
		if routeResult.React != "" {
			onEvent(ChatEvent{Type: "reaction", Data: map[string]string{"emoji": routeResult.React}})
		}
		routerMsgID := NewMessageID()
		assistantMsg := ChatMessage{
			ID:        routerMsgID,
			Role:      "assistant",
			Text:      routeResult.Response,
			Timestamp: time.Now(),
			ConvID:    req.ConvID,
			Model:     "router",
			Tier:      "router",
		}
		cs.ChatStore.Append(assistantMsg)
		if cs.ConvStore != nil {
			cs.ConvStore.Append(conversation.Message{
				ID:        routerMsgID,
				ConvID:    ccConvID,
				Channel:   conversation.ChannelCC,
				Role:      "assistant",
				Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: routeResult.Response}},
				Timestamp: assistantMsg.Timestamp,
				Model:     "router",
				Tier:      "router",
			})
		}
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

	onEvent(ChatEvent{Type: "routed", Data: map[string]string{"tier": tierName, "model": tp.Model}})

	cs.EventLog.Log("router_classify", map[string]any{
		"tier":   tierName,
		"reason": routeResult.Reason,
		"model":  tp.Model,
		"source": "api",
	})

	// Agent dispatch: delegate to multi-agent coordinator.
	if tierName == "agent" && cs.Orchestrator != nil {
		// Build conversation context from unified store.
		var convCtx string
		if cs.ConvStore != nil {
			if msgs := cs.ConvStore.Recent(conversation.ChannelCC, 0); len(msgs) > 0 {
				convCtx = conversation.BuildRouterContext(msgs, 5)
			}
		}

		orchPrep := agents.PrepareOrchestration(agents.OrchestrationInputs{
			UserMessage:          req.Message,
			DataDir:              cs.DataDir,
			ContextDir:           cs.ContextDir,
			Source:               "router",
			Model:                tp.Model,
			Backend:              tp.Backend,
			Effort:               tp.Effort,
			MaxTurns:             tp.MaxTurns,
			OrchestratorMaxTurns: tp.OrchestratorMaxTurns,
			MaxIterations:        tp.MaxIterations,
			TimeoutMin:           tp.TimeoutMin,
			RecallBlock:          recallMemories(cs.Recaller, req.Message),
			SkillStore:           cs.SkillStore,
			ConversationContext:  convCtx,
		})

		onProgress := func(phase, detail string) {
			switch phase {
			case "task_started":
				onEvent(ChatEvent{Type: "task_started", Data: map[string]string{"task_id": detail}})
			case "thinking":
				onEvent(ChatEvent{Type: "thinking", Data: map[string]string{}})
			case "planning":
				onEvent(ChatEvent{Type: "planning", Data: map[string]string{"detail": detail}})
			case "agent":
				onEvent(ChatEvent{Type: "agent_start", Data: map[string]string{"name": detail}})
			case "agent_thinking":
				onEvent(ChatEvent{Type: "agent_thinking", Data: map[string]string{"name": detail}})
			case "agent_tool":
				onEvent(ChatEvent{Type: "agent_tool", Data: map[string]string{"detail": detail}})
			case "agent_done":
				onEvent(ChatEvent{Type: "agent_done", Data: map[string]string{"detail": detail}})
			case "synthesizing":
				onEvent(ChatEvent{Type: "synthesizing", Data: map[string]string{}})
			}
		}

		orchResult, orchMeta, orchErr := cs.Orchestrator.Run(ctx, prompt, orchPrep.SystemPrompts, orchPrep.Config, onProgress)
		if orchErr != nil {
			return fmt.Errorf("agent: %w", orchErr)
		}

		agentMsgID := NewMessageID()
		assistantMsg := ChatMessage{
			ID:        agentMsgID,
			Role:      "assistant",
			Text:      orchResult,
			Timestamp: time.Now(),
			ConvID:    req.ConvID,
			Model:     "agent",
			Tier:      "agent",
			CostUSD:   orchMeta.TotalCost,
		}
		cs.ChatStore.Append(assistantMsg)
		if cs.ConvStore != nil {
			cs.ConvStore.Append(conversation.Message{
				ID:        agentMsgID,
				ConvID:    ccConvID,
				Channel:   conversation.ChannelCC,
				Role:      "assistant",
				Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: orchResult}},
				Timestamp: assistantMsg.Timestamp,
				Model:     "agent",
				Tier:      "agent",
				CostUSD:   orchMeta.TotalCost,
			})
		}

		onEvent(ChatEvent{Type: "text", Data: map[string]string{"text": orchResult}})
		onEvent(ChatEvent{Type: "done", Data: ChatDoneData{
			MsgID:   assistantMsg.ID,
			Model:   "agent",
			CostUSD: orchMeta.TotalCost,
			Tier:    "agent",
		}})

		agentText := orchResult
		if len(agentText) > 500 {
			agentText = agentText[:500]
		}
		cs.EventLog.Log("agent_out", map[string]any{
			"iterations":  orchMeta.Iterations,
			"total_cost":  orchMeta.TotalCost,
			"agent_calls": len(orchMeta.AgentCalls),
			"task_id":     orchMeta.ID,
			"text":        agentText,
			"text_length": len(orchResult),
			"source":      "api",
		})
		return nil
	}

	// Build system prompts with backend/channel/weight-aware filtering.
	isAPITier := tp.Backend != "" && tp.Backend != "cli"
	backend := "cli"
	if isAPITier {
		backend = "api"
	}
	ctxWeight := tp.EffectiveContextWeight()
	promptCfg := memory.PromptConfig{Backend: backend, Channel: "cc", Weight: ctxWeight}
	sysPromptTexts := memory.CollectPrompts(cs.ContextDir, promptCfg)
	// Inject per-tier system prompt first so it has high priority.
	if tp.SystemPrompt != "" {
		sysPromptTexts = append([]string{tp.SystemPrompt}, sysPromptTexts...)
	}
	// Inject onboarding prompt so Claude follows the onboarding instructions.
	if onboarding := memory.OnboardingPrompt(cs.ContextDir); onboarding != "" {
		sysPromptTexts = append(sysPromptTexts, onboarding)
	}
	// Auto-inject relevant memories from long-term store.
	if recallBlock := recallMemories(cs.Recaller, req.Message); recallBlock != "" {
		sysPromptTexts = append(sysPromptTexts, recallBlock)
	}
	// Inject skill catalog so the model knows available skills (skip for light tiers).
	if ctxWeight != "light" && cs.SkillStore != nil {
		if catalog := skills.BuildCatalog(cs.SkillStore); catalog != "" {
			sysPromptTexts = append(sysPromptTexts, catalog)
		}
		// Inject session-persisted skills (includes trigger-matched from earlier messages).
		if activeSkills := cs.Sessions.GetSkills(apiChatID); len(activeSkills) > 0 {
			log.Printf("[chat-api] skills: injecting session skills %v", activeSkills)
			if block := skills.BuildInjectionByName(cs.SkillStore, activeSkills); block != "" {
				sysPromptTexts = append(sysPromptTexts, block)
			}
		}
	}
	// Reaction instruction (skip for light tiers - they don't need the full format).
	if ctxWeight != "light" {
		sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(memory.ReactionMD, mood.AllowedReactionList()))
	}
	// Tool reminder at end of context (skip for light tiers).
	if ctxWeight != "light" {
		if reminder := memory.ToolReminder(cs.ContextDir); reminder != "" {
			sysPromptTexts = append(sysPromptTexts, reminder)
		}
	}

	// Inject session/conversation ID so the LLM can provide it when asked.
	if ccConvID != "" {
		sysPromptTexts = append(sysPromptTexts, fmt.Sprintf("Current session ID: %s (channel: cc)", ccConvID))
	}

	// Select provider based on tier backend.
	prov := cs.Provider
	if cs.Registry != nil {
		prov = cs.Registry.ForBackend(tp.Backend)
	}

	// Wrap API provider with agentic tool loop when tier has tools.
	if isAPITier && cs.ToolRegistry != nil && cs.ToolExecutor != nil && len(tp.Tools) > 0 {
		cs.ToolRegistry.Rescan() // pick up new tool schemas created since startup
		if apiProv, ok := prov.(*provider.APIProvider); ok {
			schemas := cs.ToolRegistry.ForToolsStrict(tp.Tools)
			if len(schemas) > 0 {
				var tools []map[string]any
				if apiProv.IsDirectOpenAI() {
					tools = tooling.ToOpenAI(schemas)
				} else {
					tools = tooling.ToOpenAICompat(schemas)
				}
				maxTurns := tp.MaxTurns
				if maxTurns <= 0 {
					maxTurns = 10
				}
				executor := &toolExecutorAdapter{exec: cs.ToolExecutor}
				prov = provider.NewToolLoop(apiProv, executor, tools, maxTurns)
				log.Printf("[chat-api] tool loop enabled: %d tools, max_turns=%d", len(schemas), maxTurns)
				toolNames := make([]string, len(schemas))
				for i, s := range schemas {
					toolNames[i] = s.Name
				}
				sysPromptTexts = append([]string{memory.ToolInstruction(toolNames)}, sysPromptTexts...)
			}
		}
	}

	// Invoke via selected Provider.
	resumeID := cs.Sessions.Get(apiChatID)
	_, lastBackend, _ := cs.Sessions.ContextFull(apiChatID)
	backendChanged := lastBackend != "" && lastBackend != tp.Backend

	// Build conversation context from unified store.
	params := provider.Params{
		Model:         tp.Model,
		Tools:         tp.Tools,
		WriteCapable:  tp.WriteCapable,
		Effort:        tp.Effort,
		MaxTurns:      tp.MaxTurns,
		SystemPrompts: sysPromptTexts,
		ResumeID:      resumeID,
		DataDir:       cs.DataDir,
	}
	if isAPITier {
		params.ResumeID = "" // API tiers use ConvMessages, not --resume
	}
	if backendChanged {
		// Backend switch: CLI --resume is stale, start fresh with injected context.
		log.Printf("[chat-api] backend switch %s→%s, dropping resume", lastBackend, tp.Backend)
		params.ResumeID = ""
	}

	// Inject conversation history from unified store.
	if cs.ConvStore != nil {
		convMsgs := conversation.BuildContext(cs.ConvStore.Recent(conversation.ChannelCC, 0), conversation.DefaultMaxMessages)
		if isAPITier || params.ResumeID == "" {
			if isAPITier {
				// API providers: pass as structured OpenAI-format messages
				// that preserve tool_calls and tool results, preventing
				// weaker models from hallucinating tool usage from text patterns.
				oaiMsgs := conversation.FlattenForOpenAI(convMsgs)
				ctxMsgs := make([]provider.ContextMessage, len(oaiMsgs))
				for i, m := range oaiMsgs {
					cm := provider.ContextMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
					for _, tc := range m.ToolCalls {
						cm.ToolCalls = append(cm.ToolCalls, provider.ContextToolCall{
							ID:        tc.ID,
							Name:      tc.Name,
							Arguments: tc.Arguments,
						})
					}
					ctxMsgs[i] = cm
				}
				params.ConvMessages = ctxMsgs
			} else {
				// CLI without resume: inject as system prompt.
				if histPrompt := conversation.FormatAsSystemPrompt(convMsgs, ctxWeight); histPrompt != "" {
					params.SystemPrompts = append(params.SystemPrompts, histPrompt)
				}
			}
		}
		// CLI with --resume: skip injection, CLI has its own richer context.
	}

	var rawProgressFn provider.OnProgress
	rawProgressFn = func(event provider.StreamEvent) {
		switch event.Type {
		case "thinking":
			if event.Text != "" {
				onEvent(ChatEvent{Type: "thinking", Data: map[string]string{"text": event.Text}})
			} else {
				onEvent(ChatEvent{Type: "thinking", Data: map[string]string{}})
			}
		case "tool_use":
			onEvent(ChatEvent{Type: "tool_use", Data: map[string]string{"name": event.Detail}})
		case "tool_input":
			onEvent(ChatEvent{Type: "tool_input", Data: map[string]string{"name": event.Detail, "chunk": event.Text}})
		case "tool_result":
			onEvent(ChatEvent{Type: "tool_result", Data: map[string]string{"tool_id": event.Detail, "result": event.Text}})
		case "text_delta":
			onEvent(ChatEvent{Type: "text_delta", Data: map[string]string{"text": event.Text}})
		}
	}

	// Wrap with accumulator to capture content blocks for the conversation store.
	var acc *conversation.Accumulator
	progressFn := rawProgressFn
	if cs.ConvStore != nil {
		acc = conversation.NewAccumulator()
		progressFn = acc.OnProgress(rawProgressFn)
	}

	start := time.Now()
	result, err := prov.Invoke(ctx, prompt, params, progressFn)

	// Retry without resume if session failed (CLI only).
	if err != nil && resumeID != "" && !isAPITier {
		log.Printf("[chat-api] session %s failed (%v), starting fresh", resumeID, err)
		cs.Sessions.Archive(apiChatID)
		params.ResumeID = ""
		// Inject conversation history since we lost --resume context.
		if cs.ConvStore != nil {
			convMsgs := conversation.BuildContext(cs.ConvStore.Recent(conversation.ChannelCC, 0), conversation.DefaultMaxMessages)
			if histPrompt := conversation.FormatAsSystemPrompt(convMsgs, ctxWeight); histPrompt != "" {
				params.SystemPrompts = append(params.SystemPrompts, histPrompt)
			}
		}
		// Reset accumulator for the retry.
		if acc != nil {
			acc = conversation.NewAccumulator()
			progressFn = acc.OnProgress(rawProgressFn)
		}
		result, err = prov.Invoke(ctx, prompt, params, progressFn)
	}
	duration := time.Since(start)

	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}

	sessShort := result.SessionID
	if len(sessShort) > 8 {
		sessShort = sessShort[:8]
	}
	// Compute cost from tokens if not already set (API backends).
	if result.CostUSD == 0 && result.InputTokens > 0 && cs.BackendConfigs != nil {
		if bc, ok := cs.BackendConfigs()[tp.Backend]; ok && (bc.InputPrice > 0 || bc.OutputPrice > 0) {
			result.CostUSD = float64(result.InputTokens)/1e6*bc.InputPrice +
				float64(result.OutputTokens)/1e6*bc.OutputPrice
		}
	}

	log.Printf("[chat-api] → %s %dms %dt/%dt $%.4f sid:%s", result.Model, duration.Milliseconds(), result.InputTokens, result.OutputTokens, result.CostUSD, sessShort)

	// Update session.
	if result.SessionID != "" {
		cs.Sessions.SetWithBackend(apiChatID, result.SessionID, tierName, tp.Backend)
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
	assistantMsgID := NewMessageID()
	assistantMsg := ChatMessage{
		ID:        assistantMsgID,
		Role:      "assistant",
		Text:      cleanText,
		Timestamp: time.Now(),
		ConvID:    req.ConvID,
		Model:     result.Model,
		Tier:      tierName,
		CostUSD:   result.CostUSD,
		SessionID: result.SessionID,
	}
	cs.ChatStore.Append(assistantMsg)

	// Parallel write to unified conversation store with rich content blocks.
	if cs.ConvStore != nil {
		var blocks []conversation.ContentBlock
		if acc != nil {
			blocks = acc.Blocks()
		}
		if len(blocks) == 0 {
			// Fallback: store as plain text block.
			blocks = []conversation.ContentBlock{{Type: conversation.BlockText, Text: cleanText}}
		}
		cs.ConvStore.Append(conversation.Message{
			ID:        assistantMsgID,
			ConvID:    ccConvID,
			Channel:   conversation.ChannelCC,
			Role:      "assistant",
			Blocks:    blocks,
			Timestamp: assistantMsg.Timestamp,
			Model:     result.Model,
			Tier:      tierName,
			Backend:   tp.Backend,
			CostUSD:   result.CostUSD,
			SessionID: result.SessionID,
		})
	}

	// Send text to client.
	onEvent(ChatEvent{Type: "text", Data: map[string]string{"text": cleanText}})

	doneData := ChatDoneData{
		MsgID:      assistantMsg.ID,
		SessionID:  result.SessionID,
		Model:      result.Model,
		CostUSD:    result.CostUSD,
		Tier:       tierName,
		DurationMs: duration.Milliseconds(),
	}
	if sk := cs.Sessions.GetSkills(apiChatID); len(sk) > 0 {
		doneData.Skills = sk
	}
	onEvent(ChatEvent{Type: "done", Data: doneData})

	// Warn when context is getting large (message count proxy).
	if _, msgCount := cs.Sessions.Context(apiChatID); msgCount >= 20 {
		level := "high"
		if msgCount >= 40 {
			level = "critical"
		}
		onEvent(ChatEvent{Type: "system", Data: map[string]string{
			"text": fmt.Sprintf("⚠️ Context is getting large (%d messages). Consider using /new to start fresh.", msgCount),
			"level": level,
		}})
	}

	outText := cleanText
	if len(outText) > 500 {
		outText = outText[:500]
	}
	cs.EventLog.Log("message_out", map[string]any{
		"model":       result.Model,
		"cost_usd":    result.CostUSD,
		"text":        outText,
		"text_length": len(cleanText),
		"session_id":  result.SessionID,
		"tier":        tierName,
		"source":      "api",
	})

	return nil
}

// askViaEngine delegates message processing to the unified comms engine.
// ChatStore writes and ChatDoneData emission remain CC-specific.
func (cs *ChatService) askViaEngine(ctx context.Context, req ChatRequest, onEvent func(ChatEvent)) error {
	channelID := comms.ChannelID("cc:" + req.ConvID)
	if req.ConvID == "" {
		channelID = "cc:default"
	}

	// 0. Built-in command handling via comms engine (/new, /skills, etc.).
	if cs.Engine != nil && strings.HasPrefix(req.Message, "/") {
		if response, handled := cs.Engine.HandleCommand(channelID, req.Message); handled {
			if response != "" {
				onEvent(ChatEvent{Type: "system", Data: map[string]string{"text": response}})
			}
			onEvent(ChatEvent{Type: "done", Data: ChatDoneData{}})
			return nil
		}
	}

	// 1. Force command detection: /<tier> or /<skill> (CC-specific).
	if strings.HasPrefix(req.Message, "/") && req.Model == "" {
		parts := strings.SplitN(req.Message, " ", 2)
		cmdName := strings.TrimPrefix(parts[0], "/")

		// 1a. Tier force commands.
		for _, t := range cs.TierStore.Current().Tiers {
			if t.Enabled && t.ForceCommand && t.Name == cmdName {
				cs.Sessions.SetForcedTier(apiChatID, t.Name)
				onEvent(ChatEvent{Type: "system", Data: map[string]string{
					"text": fmt.Sprintf("⚡ Session locked to **%s**. Use /new to reset.", t.Name),
				}})
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					onEvent(ChatEvent{Type: "done", Data: ChatDoneData{Model: t.Name, Tier: t.Name}})
					return nil
				}
				req.Model = t.Name
				req.Message = strings.TrimSpace(parts[1])
				break
			}
		}

		// 1b. Skill force commands: /skillname [message]
		if cs.Engine != nil && cs.Engine.SkillStore != nil && req.Model == "" {
			if sk, ok := cs.Engine.SkillStore.Get(cmdName); ok {
				sessionKey := channelID.SessionKey()
				cs.Engine.Sessions.AddSkills(sessionKey, []string{sk.Name})
				desc := sk.Description
				if desc != "" {
					desc = " — " + desc
				}
				onEvent(ChatEvent{Type: "system", Data: map[string]string{
					"text": fmt.Sprintf("🧩 Skill **%s** activated%s", sk.Name, desc),
				}})
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					onEvent(ChatEvent{Type: "done", Data: ChatDoneData{}})
					return nil
				}
				req.Message = strings.TrimSpace(parts[1])
			}
		}
	}

	// Check persistent tier override.
	if req.Model == "" {
		if ft := cs.Sessions.GetForcedTier(apiChatID); ft != "" {
			req.Model = ft
		}
	}

	// 2. Build prompt (CC-specific: upload registry, reply context from ChatStore).
	prompt := cs.buildPrompt(req)
	if prompt == "" {
		return fmt.Errorf("empty message")
	}

	// Build router text (raw message + brief quote hint for router).
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

	// 3. Save user message to ChatStore (CC-specific parallel store).
	userMsgID := NewMessageID()
	userMsg := ChatMessage{
		ID:        userMsgID,
		Role:      "user",
		Text:      req.Message,
		Timestamp: time.Now(),
		ConvID:    req.ConvID,
		ReplyTo:   req.ReplyTo,
		Media:     cs.resolveMediaRefs(req.MediaIDs),
	}
	cs.ChatStore.Append(userMsg)

	// 4. Build InMessage for engine.
	msg := comms.InMessage{
		ChannelID:  channelID,
		Text:       prompt,
		RawText:    req.Message,
		RouterText: routerMsg,
		IsReply:    req.ReplyTo != "",
		ForcedTier: req.Model,
	}
	if req.ReplyTo != "" {
		if orig := cs.ChatStore.Get(req.ReplyTo); orig != nil {
			msg.ReplyTo = orig.Text
		}
	}

	// 5. Set up event bridge (suppress engine's "done", we emit our own).
	cs.ccAdapter.setCallback(onEvent)
	defer cs.ccAdapter.setCallback(nil)

	// 6. Call engine.Process().
	start := time.Now()
	result, err := cs.Engine.Process(ctx, msg)
	duration := time.Since(start)

	if err != nil {
		return err
	}

	// 7. Spontaneous mood reaction (CC-specific).
	state := mood.GetCurrentState(cs.ContextDir)
	if mood.ShouldReact(state) {
		spontaneous := mood.ChooseSpontaneous(state)
		if spontaneous != "" {
			onEvent(ChatEvent{Type: "reaction", Data: map[string]string{"emoji": spontaneous}})
		}
	}

	// 8. Save assistant message to ChatStore (CC-specific parallel store).
	assistantMsgID := NewMessageID()
	assistantMsg := ChatMessage{
		ID:        assistantMsgID,
		Role:      "assistant",
		Text:      result.Text,
		Timestamp: time.Now(),
		ConvID:    req.ConvID,
		Model:     result.Model,
		Tier:      result.Tier,
		CostUSD:   result.CostUSD,
		SessionID: result.SessionID,
	}
	cs.ChatStore.Append(assistantMsg)

	// 9. Emit "done" with ChatDoneData (CC-specific rich format).
	doneData := ChatDoneData{
		MsgID:      assistantMsgID,
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
		if cs.ConvStore != nil {
			cs.ConvStore.NewConversation(conversation.ChannelCC)
		}
		if onboard {
			memory.SetOnboarding(cs.ContextDir)
		} else {
			memory.ClearOnboarding(cs.ContextDir)
		}
	}
	var newConvID string
	if cs.ConvStore != nil {
		newConvID = cs.ConvStore.ConvID(conversation.ChannelCC)
	}
	if newConvID == "" {
		newConvID = NewMessageID()
	}
	return old, newConvID
}

// ActiveSkills returns the names of skills currently active in the CC session.
func (cs *ChatService) ActiveSkills() []string {
	return cs.Sessions.GetSkills(apiChatID)
}

// ClearActiveSkills removes all active skills from the CC session.
func (cs *ChatService) ClearActiveSkills() {
	cs.Sessions.ClearSkills(apiChatID)
}

// CurrentConvID returns the active conversation ID for the CC channel.
func (cs *ChatService) CurrentConvID() string {
	if cs.ConvStore != nil {
		return cs.ConvStore.ConvID(conversation.ChannelCC)
	}
	return ""
}

// History returns paginated chat history, optionally filtered by conversation.
func (cs *ChatService) History(limit int, before time.Time, convID string) []ChatMessage {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	msgs := cs.ChatStore.History(limit, before, convID)
	if msgs == nil {
		return []ChatMessage{}
	}
	return msgs
}

// Conversations returns all known conversation summaries.
func (cs *ChatService) Conversations() []ConversationInfo {
	return cs.ChatStore.Conversations()
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
				tools = tooling.DiscoverToolNames(cs.DataDir)
				if cs.ToolRegistry != nil {
					tools = append(tools, cs.ToolRegistry.NativeToolNames()...)
				}
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
	if fallback := cs.firstFallbackTier(); fallback != "" {
		tp = cs.resolveTierParams(fallback)
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
const recallDistanceThreshold = 1.2
const recallLimit = 3

// StartJob launches Ask in a background goroutine and returns the job for streaming.
// If a job is already running for the same conversation, returns it for reconnection.
func (cs *ChatService) StartJob(req ChatRequest) *chatJob {
	convID := req.ConvID
	cs.jobMu.Lock()
	if j := cs.activeJobs[convID]; j != nil && !j.isDone() {
		cs.jobMu.Unlock()
		return j
	}

	ctx, cancel := context.WithCancel(context.Background())
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

	go func() {
		err := cs.Ask(ctx, req, func(evt ChatEvent) {
			job.push(evt)
		})
		job.finish(err)
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

// toolExecutorAdapter bridges tooling.Executor to provider.ToolExecutor.
type toolExecutorAdapter struct {
	exec *tooling.Executor
}

func (a *toolExecutorAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
	result := a.exec.Execute(ctx, tooling.CallRequest{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	return provider.ToolCallResult{
		ID:      result.ID,
		Output:  result.Output,
		IsError: result.IsError,
	}
}
