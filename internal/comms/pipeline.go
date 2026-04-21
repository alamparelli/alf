package comms

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	provider "github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/mood"
	agents "github.com/alamparelli/alf/internal/runtime/agents"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
	"github.com/alamparelli/alf/internal/trace"
)

// Process handles an incoming message through the full pipeline.
// The adapter is responsible for calling this method and translating OutEvents
// into platform-specific output (SSE, Telegram messages, etc.).
func (e *ChatEngine) Process(ctx context.Context, msg InMessage) (*ProcessResult, error) {
	channelID := msg.ChannelID
	channel := channelID.ConvChannel()
	sessionKey := channelID.SessionKey()

	// 0. Create request tracer.
	// If the adapter pre-inserted the user message, reuse its ID so tracer,
	// and the Memory store remain consistent (#310).
	userMsgID := msg.PreInsertedUserMsgID
	if userMsgID == "" {
		userMsgID = string(memory.NewMessageID())
	}
	// convID is the unified conversation ID used by the memory store. For CC
	// callers it is msg.ConvID (tab-scoped). For TG callers it defaults to
	// the channel-scoped active conv pref.
	convID := msg.ConvID
	if convID == "" && e.Memory != nil {
		v, _ := e.Memory.GetPref(ctx, "active_conv:"+string(channelID))
		if s, ok := v.(string); ok && s != "" {
			convID = s
		} else {
			newID := memory.NewConvID()
			_ = e.Memory.SetPref(ctx, "active_conv:"+string(channelID), string(newID))
			convID = string(newID)
		}
	}
	tracer := trace.New(channelID.Prefix(), convID, userMsgID)
	ctx = trace.WithContext(ctx, tracer)
	defer tracer.Flush(e.DataDir)

	// 1. Persist user message to Memory (skip if adapter did it).
	// Phase-1 parallel write (ChatDB + ConvStore) collapsed into a single
	// Memory.AppendMessage call — single schema, one write.
	if e.Memory != nil && msg.PreInsertedUserMsgID == "" && convID != "" {
		_ = e.Memory.EnsureConv(ctx, memory.ConvID(convID), "", msg.Source)
		userMem := memory.Message{
			Role:    "user",
			Channel: channel,
			Content: msg.DisplayText(),
			Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: msg.DisplayText()}},
			ReplyTo: memory.MsgID(msg.ReplyToMsgID),
		}
		if stored, err := e.Memory.AppendMessage(ctx, memory.ConvID(convID), userMem); err == nil && stored.ID != "" {
			userMsgID = string(stored.ID)
		}
	}

	// 1b. Log incoming message to EventLog for mem-extract.
	if e.EventLog != nil {
		e.EventLog.Log("message_in", map[string]any{
			"channel":    string(channel),
			"session_id": sessionKey,
			"text":       msg.DisplayText(),
			"is_reply":   msg.IsReply,
			"has_media":  len(msg.Media) > 0,
		})
	}

	// 2. Check force command / session tier override.
	routeSpan := tracer.StartSpan("route", nil)
	forcedTier := msg.ForcedTier
	if forcedTier == "" {
		if ft := e.Sessions.GetForcedTier(sessionKey); ft != "" {
			forcedTier = ft
		}
	}

	// 3. Pre-route memory recall.
	recall := RecallWithConfig(e.Recaller, msg.Text, e.RecallCfg)

	// 4. Classify message.
	lastTier, msgCount := e.Sessions.Context(sessionKey)

	var recentCtx string
	if e.Memory != nil {
		recentCtx = memory.BuildRouterContext(collectRecentAll(ctx, e.Memory, 6), 3)
	}

	var route RouteResult
	hasMedia := len(msg.Media) > 0
	routerMsg := msg.RouterText
	if routerMsg == "" {
		routerMsg = msg.Text
	}

	if forcedTier != "" {
		// Forced tier bypasses routing.
		route = RouteResult{Tier: forcedTier, Reason: "force_command"}
		log.Printf("[comms] → force command → tier %q", forcedTier)
	} else if hasMedia {
		// Media routing: classify if caption, else cheapest with Read.
		route = e.routeMedia(routerMsg, lastTier, msgCount, recentCtx)
	} else {
		route = e.ClassifyFull(routerMsg, lastTier, msgCount, recentCtx)
	}

	// Early exit if context was cancelled during routing.
	if ctx.Err() != nil {
		routeSpan.End()
		return nil, ctx.Err()
	}

	// 5. Post-route adjustments.
	// Reply reclassification: if reply + direct response → re-classify with context.
	if msg.IsReply && forcedTier == "" && route.Response != "" && route.Tier == "" {
		originalResult := route
		replyHint := msg.Text
		if msg.ReplyTo != "" {
			replyHint = fmt.Sprintf("[The user is replying to:\n---\n%s\n---\n]\n%s", msg.ReplyTo, msg.Text)
		}
		replyHint += "\n[CONTEXT: This is a reply to a previous assistant message. Route to an appropriate tier - do not respond directly.]"
		reclassified := e.ClassifyFull(replyHint, lastTier, msgCount, recentCtx)
		if reclassified.Tier != "" {
			route = reclassified
			route.Reason = "reply-reclassify: " + reclassified.Reason
		} else {
			fallback := FirstFallbackTier(e.TierStore)
			route = RouteResult{Tier: fallback, Reason: fmt.Sprintf("reply-fallback: %s→%s", originalResult.Tier, fallback)}
		}
		log.Printf("[comms] → reply re-routed: %s → %s (%s)", originalResult.Tier, route.Tier, route.Reason)
	}

	// Memory recall escalation: if highly relevant (dist < 0.6), override direct response.
	if recall.Block != "" && recall.BestDist < 0.6 && route.Response != "" && route.Tier == "" {
		log.Printf("[comms] → memory override: direct response upgraded to tier (best_dist=%.2f)", recall.BestDist)
		fallback := FirstFallbackTier(e.TierStore)
		route = RouteResult{Tier: fallback, Reason: "memory-override: direct→" + fallback}
	}

	// Skill force command: /skillname [message]
	if e.SkillStore != nil && strings.HasPrefix(msg.Text, "/") {
		parts := strings.SplitN(msg.Text, " ", 2)
		cmdName := strings.TrimPrefix(parts[0], "/")
		if sk, ok := e.SkillStore.Get(cmdName); ok {
			e.Sessions.AddSkills(sessionKey, []string{sk.Name})
			log.Printf("[comms] skills: force-activated %q via /%s", sk.Name, cmdName)
			if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
				msg.Text = strings.TrimSpace(parts[1])
			}
		}
	}

	// Skill trigger matching.
	if e.SkillStore != nil {
		if triggerMatched := skills.MatchTriggers(e.SkillStore, msg.Text); len(triggerMatched) > 0 {
			triggerNames := make([]string, len(triggerMatched))
			for i, sk := range triggerMatched {
				triggerNames[i] = sk.Name
			}
			log.Printf("[comms] skills: trigger-matched %v", triggerNames)
			e.Sessions.AddSkills(sessionKey, triggerNames)
		}
	}

	// Skill tier override.
	if activeSkills := e.Sessions.GetSkills(sessionKey); len(activeSkills) > 0 {
		if minTier := skills.ResolveMinTier(e.SkillStore, activeSkills); minTier != "" {
			route = e.applySkillTierOverride(route, minTier)
		}
	}

	// Onboarding override.
	if memory.OnboardingPrompt(e.ContextDir) != "" {
		fallback := OnboardingTier(e.TierStore)
		log.Printf("[comms] → onboarding override: %q → tier %q", route.Tier, fallback)
		route = RouteResult{Tier: fallback, Reason: "onboarding-override: " + fallback}
	}

	// 6. Direct response — no second LLM call needed.
	if route.Response != "" && route.Tier == "" {
		routeSpan.Tag("tier", "router")
		routeSpan.Tag("reason", route.Reason)
		routeSpan.Tag("direct", "true")
		routeSpan.End()
		e.Sessions.TouchContext(sessionKey, "router")
		routerMsgID := string(memory.NewMessageID())
		// Persist to Memory (unified store replaces parallel ChatDB+ConvStore writes).
		if e.Memory != nil && convID != "" {
			asst := memory.Message{
				Role:    "assistant",
				Channel: channel,
				Content: route.Response,
				Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: route.Response}},
				Model:   "router",
				Tier:    "router",
			}
			if stored, err := e.Memory.AppendMessage(ctx, memory.ConvID(convID), asst); err == nil && stored.ID != "" {
				routerMsgID = string(stored.ID)
			}
		}
		e.emit(channelID, OutEvent{Type: "text", Data: map[string]string{"text": route.Response}})
		if route.React != "" {
			e.emit(channelID, OutEvent{Type: "reaction", Data: map[string]string{"emoji": route.React}})
		}
		e.emit(channelID, OutEvent{Type: "done", Data: map[string]string{
			"model": "router",
			"tier":  "router",
		}})
		return &ProcessResult{
			Text:           route.Response,
			Model:          "router",
			Tier:           "router",
			Reason:         route.Reason,
			Reaction:       route.React,
			UserMsgID:      userMsgID,
			AssistantMsgID: routerMsgID,
		}, nil
	}

	// 7. Validate selected tier.
	tiers := e.TierStore.Snapshot()
	if route.Tier != "" && forcedTier == "" {
		if !IsTierValid(route.Tier, tiers) {
			fallback := FirstFallbackTier(e.TierStore)
			log.Printf("[comms] → tier %q not routable/enabled, falling back → %s", route.Tier, fallback)
			route = RouteResult{Tier: fallback, Reason: fmt.Sprintf("tier-invalid: %s→%s", route.Tier, fallback)}
		}
	}

	// 8. Resolve tier params.
	tp, _ := ResolveTierParams(route.Tier, tiers, e.DataDir, e.ToolRegistry, e.Registry, e.ResolveModel)

	routeSpan.Tag("tier", route.Tier)
	routeSpan.Tag("reason", route.Reason)
	routeSpan.Tag("model", tp.Model)
	if forcedTier != "" {
		routeSpan.Tag("forced", "true")
	}
	routeSpan.End()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	e.emit(channelID, OutEvent{Type: "routed", Data: map[string]string{"tier": route.Tier, "model": tp.Model}})

	if e.EventLog != nil {
		e.EventLog.Log("router_classify", map[string]any{
			"tier":    route.Tier,
			"reason":  route.Reason,
			"model":   tp.Model,
			"channel": channelID.Prefix(),
		})
	}

	// 9. Agent dispatch — check by role, not name.
	if tiers.IsOrchestratorTier(route.Tier) && e.Orchestrator != nil {
		// Require a model to be explicitly set on the orchestrator tier.
		if tp.Model == "" {
			log.Printf("[comms] → orchestrator tier %q has no model configured — aborting", route.Tier)
			e.emit(channelID, OutEvent{Type: "text", Data: map[string]string{"text": "⚠️ Orchestrator tier has no model configured. Please set a model (e.g. \"sonnet\") in your orchestrator tier config."}})
			e.emit(channelID, OutEvent{Type: "done", Data: map[string]string{"tier": route.Tier, "model": ""}})
			return nil, nil
		}
		// Skip orchestrator if no teams are configured — fallback to next tier.
		if !e.Orchestrator.HasTeams() {
			fallback := FirstFallbackTier(e.TierStore)
			log.Printf("[comms] → orchestrator tier %q skipped (no teams configured), falling back → %s", route.Tier, fallback)
			route = RouteResult{Tier: fallback, Reason: fmt.Sprintf("no-teams: %s→%s", route.Tier, fallback)}
			tp, _ = ResolveTierParams(route.Tier, tiers, e.DataDir, e.ToolRegistry, e.Registry, e.ResolveModel)
		} else {
			return e.processAgent(ctx, msg, tp, recall, convID, userMsgID)
		}
	}

	// 10-13. Standard provider invocation.
	return e.processStandard(ctx, msg, tp, route, recall, convID, userMsgID)
}

// routeMedia handles media routing: classify if caption, else cheapest with Read.
func (e *ChatEngine) routeMedia(routerMsg, lastTier string, msgCount int, recentCtx string) RouteResult {
	tiers := e.TierStore.Snapshot()
	if routerMsg != "" {
		route := e.ClassifyFull(routerMsg, lastTier, msgCount, recentCtx)
		log.Printf("[comms] → media+caption: router chose tier=%q reason=%q", route.Tier, route.Reason)
		// Ensure tier has Read capability.
		needsUpgrade := false
		if route.Tier == "" || route.Response != "" {
			needsUpgrade = true
		} else {
			for _, t := range tiers.Tiers {
				if t.Name == route.Tier && !TierHasRead(t) {
					needsUpgrade = true
					break
				}
			}
		}
		if needsUpgrade {
			upgraded := LowestMediaTier(tiers)
			log.Printf("[comms] → media upgrade: %q → %q (needs Read tool)", route.Tier, upgraded)
			return RouteResult{Tier: upgraded, Reason: fmt.Sprintf("media-upgrade: %s→%s", route.Tier, upgraded)}
		}
		return route
	}
	tierName := LowestMediaTier(tiers)
	log.Printf("[comms] → media (no caption), bypassing router → tier %q", tierName)
	return RouteResult{Tier: tierName, Reason: "media bypass (no caption)"}
}

// applySkillTierOverride forces routing to the skill's required tier if needed.
func (e *ChatEngine) applySkillTierOverride(route RouteResult, minTier string) RouteResult {
	if route.Response != "" && route.Tier == "" {
		log.Printf("[comms] skill tier override: direct→%s", minTier)
		return RouteResult{Tier: minTier, Reason: "skill-tier: " + minTier}
	}
	if route.Tier != "" && route.Tier != minTier {
		currentPri, requiredPri := -1, -1
		for _, t := range e.TierStore.Snapshot().Tiers {
			if t.Name == route.Tier {
				currentPri = t.Priority
			}
			if t.Name == minTier {
				requiredPri = t.Priority
			}
		}
		if requiredPri >= 0 && currentPri < requiredPri {
			old := route.Tier
			log.Printf("[comms] skill tier override: %s→%s", old, minTier)
			return RouteResult{Tier: minTier, Reason: fmt.Sprintf("skill-tier: %s→%s", old, minTier)}
		}
	}
	return route
}

// processAgent delegates to the multi-agent orchestrator.
func (e *ChatEngine) processAgent(ctx context.Context, msg InMessage, tp TierParams, recall RecallResult, convID string, userMsgID string) (*ProcessResult, error) {
	channelID := msg.ChannelID
	channel := channelID.ConvChannel()

	var convCtx string
	if e.Memory != nil && convID != "" {
		if msgs, _ := e.Memory.ListMessages(ctx, memory.ConvID(convID), memory.ListOpts{ApplySummary: true}); len(msgs) > 0 {
			convCtx = memory.BuildRouterContext(msgs, 5)
		}
	}

	orchPrep := agents.PrepareOrchestration(agents.OrchestrationInputs{
		UserMessage:          msg.Text,
		DataDir:              e.DataDir,
		ContextDir:           e.ContextDir,
		Source:               channelID.Prefix(),
		Model:                tp.Model,
		Backend:              tp.Backend,
		Effort:               tp.Effort,
		MaxTurns:             tp.MaxTurns,
		OrchestratorMaxTurns: tp.OrchestratorMaxTurns,
		MaxIterations:        tp.MaxIterations,
		TimeoutMin:           tp.TimeoutMin,
		RecallBlock:          recall.Block,
		SkillStore:           e.SkillStore,
		ConversationContext:  convCtx,
	})

	onProgress := func(phase, detail string) {
		switch phase {
		case "task_started":
			e.emit(channelID, OutEvent{Type: "task_started", Data: map[string]string{"task_id": detail}})
		case "thinking":
			e.emit(channelID, OutEvent{Type: "thinking", Data: map[string]string{}})
		case "planning":
			e.emit(channelID, OutEvent{Type: "planning", Data: map[string]string{"detail": detail}})
		case "agent":
			e.emit(channelID, OutEvent{Type: "agent_start", Data: map[string]string{"name": detail}})
		case "agent_thinking":
			e.emit(channelID, OutEvent{Type: "agent_thinking", Data: map[string]string{"name": detail}})
		case "agent_tool":
			e.emit(channelID, OutEvent{Type: "agent_tool", Data: map[string]string{"detail": detail}})
		case "agent_done":
			e.emit(channelID, OutEvent{Type: "agent_done", Data: map[string]string{"detail": detail}})
		case "synthesizing":
			e.emit(channelID, OutEvent{Type: "synthesizing", Data: map[string]string{}})
		}
	}

	agentSpan := trace.StartSpanFromContext(ctx, "agent_run", map[string]string{
		"model": tp.Model, "tier": "agent",
	})

	start := time.Now()
	orchResult, orchMeta, orchErr := e.Orchestrator.Run(ctx, msg.Text, orchPrep.SystemPrompts, orchPrep.Config, onProgress)
	duration := time.Since(start)

	if agentSpan != nil {
		agentSpan.Tag("duration_ms", fmt.Sprintf("%d", duration.Milliseconds()))
		if orchMeta != nil {
			agentSpan.Tag("iterations", fmt.Sprintf("%d", orchMeta.Iterations))
			agentSpan.Tag("total_cost", fmt.Sprintf("%.4f", orchMeta.TotalCost))
			agentSpan.Tag("agent_calls", fmt.Sprintf("%d", len(orchMeta.AgentCalls)))
			agentSpan.Tag("task_id", orchMeta.ID)
		}
		if orchErr != nil {
			agentSpan.EndWithError(orchErr)
		} else {
			agentSpan.End()
		}
	}

	if orchErr != nil {
		e.emit(channelID, OutEvent{Type: "error", Data: map[string]string{"text": orchErr.Error()}})
		return nil, fmt.Errorf("agent: %w", orchErr)
	}

	// Persist agent response (collapsed parallel write into Memory.AppendMessage).
	agentMsgID := string(memory.NewMessageID())
	if e.Memory != nil && convID != "" {
		asst := memory.Message{
			Role:       "assistant",
			Channel:    channel,
			Content:    orchResult,
			Blocks:     []memory.ContentBlock{{Type: memory.BlockText, Text: orchResult}},
			Model:      "agent",
			Tier:       "agent",
			Backend:    tp.Backend,
			CostUSD:    orchMeta.TotalCost,
			DurationMs: duration.Milliseconds(),
		}
		if stored, err := e.Memory.AppendMessage(ctx, memory.ConvID(convID), asst); err == nil && stored.ID != "" {
			agentMsgID = string(stored.ID)
		}
		// Trigger progressive summarization on the agent path too (mirrors processStandard).
		e.maybeSummarizeAsync(ctx, channel, convID)
	}

	e.emit(channelID, OutEvent{Type: "text", Data: map[string]string{"text": orchResult}})
	e.emit(channelID, OutEvent{Type: "done", Data: map[string]string{
		"model":   "agent",
		"tier":    "agent",
		"cost":    fmt.Sprintf("%.4f", orchMeta.TotalCost),
		"task_id": orchMeta.ID,
	}})

	if e.EventLog != nil {
		agentText := orchResult
		if len(agentText) > 500 {
			agentText = agentText[:500]
		}
		e.EventLog.Log("agent_out", map[string]any{
			"iterations":  orchMeta.Iterations,
			"total_cost":  orchMeta.TotalCost,
			"agent_calls": len(orchMeta.AgentCalls),
			"task_id":     orchMeta.ID,
			"text":        agentText,
			"text_length": len(orchResult),
			"channel":     channelID.Prefix(),
			"duration_ms": duration.Milliseconds(),
		})
	}

	return &ProcessResult{
		Text:           orchResult,
		Model:          "agent",
		Tier:           "agent",
		CostUSD:        orchMeta.TotalCost,
		IsAgent:        true,
		Duration:       duration.Milliseconds(),
		UserMsgID:      userMsgID,
		AssistantMsgID: agentMsgID,
	}, nil
}

// processStandard handles non-agent tier invocation (CLI or API provider).
func (e *ChatEngine) processStandard(ctx context.Context, msg InMessage, tp TierParams, route RouteResult, recall RecallResult, convID string, userMsgID string) (*ProcessResult, error) {
	channelID := msg.ChannelID
	channel := channelID.ConvChannel()
	sessionKey := channelID.SessionKey()

	// Build system prompts.
	isAPITier := tp.Backend != "" && tp.Backend != "cli"
	backend := "cli"
	if isAPITier {
		backend = "api"
	}
	ctxWeight := tp.EffectiveContextWeight()
	promptCfg := memory.PromptConfig{Backend: backend, Channel: channelID.Prefix(), Weight: ctxWeight}
	// Pass the actual backend name so conditional sections like <!-- @begin codex --> work.
	if tp.Backend != "" {
		promptCfg.BackendName = tp.Backend
	}
	sysPrompts := memory.CollectPrompts(e.ContextDir, promptCfg)

	// Tier system prompt.
	if tp.SystemPrompt != "" {
		sysPrompts = append([]string{tp.SystemPrompt}, sysPrompts...)
	}
	// Inject router label as role context so the tier knows its assigned role.
	if tp.RouterLabel != "" {
		rolePrompt := "Your assigned role: " + tp.RouterLabel
		sysPrompts = append([]string{rolePrompt}, sysPrompts...)
	}
	// Onboarding prompt.
	if onboarding := memory.OnboardingPrompt(e.ContextDir); onboarding != "" {
		sysPrompts = append(sysPrompts, onboarding)
	}
	// Cache breakpoint: everything before this is stable and cacheable.
	cacheBreakpoint := len(sysPrompts)

	// Current time (dynamic — changes every minute; must be after breakpoint).
	sysPrompts = append(sysPrompts, "Current time: "+time.Now().Format("15:04"))

	// Memory recall (dynamic — changes per request).
	if recall.Block != "" {
		sysPrompts = append(sysPrompts, recall.Block)
	}
	// Skills (skip for light tiers).
	if ctxWeight != "light" && e.SkillStore != nil {
		if catalog := skills.BuildCatalog(e.SkillStore); catalog != "" {
			sysPrompts = append(sysPrompts, catalog)
		}
		if activeSkills := e.Sessions.GetSkills(sessionKey); len(activeSkills) > 0 {
			log.Printf("[comms] skills: injecting session skills %v", activeSkills)
			if block := skills.BuildInjectionByName(e.SkillStore, activeSkills); block != "" {
				sysPrompts = append(sysPrompts, block)
			}
		}
	}
	// Reaction instruction.
	if ctxWeight != "light" {
		sysPrompts = append(sysPrompts, fmt.Sprintf(memory.ReactionMD, mood.AllowedReactionList()))
	}
	// Session ID.
	if convID != "" {
		sysPrompts = append(sysPrompts, fmt.Sprintf("Current session ID: %s (channel: %s)", convID, channelID.Prefix()))
	}

	// Select provider.
	prov := e.Registry.ForBackend(tp.Backend)

	// Wrap API provider with agentic tool loop.
	if isAPITier && e.ToolRegistry != nil && e.ToolExecutor != nil && len(tp.Tools) > 0 {
		e.ToolRegistry.Rescan()
		if apiProv, ok := prov.(*provider.APIProvider); ok {
			schemas := e.ToolRegistry.ForToolsStrict(tp.Tools)
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
				prov = provider.NewToolLoop(apiProv, &toolExecAdapter{exec: e.ToolExecutor, origin: tooling.ChainOrigin{Source: channelID.Prefix(), ConvID: convID}}, tools, maxTurns)
				log.Printf("[comms] tool loop enabled: %d tools, max_turns=%d", len(schemas), maxTurns)
				toolNames := make([]string, len(schemas))
				for i, s := range schemas {
					toolNames[i] = s.Name
				}
				sysPrompts = append([]string{memory.ToolInstruction(toolNames)}, sysPrompts...)
				cacheBreakpoint++ // tool instruction was prepended; shift breakpoint to keep stable/dynamic split correct
			}
		}
	}

	// Resolve resume ID.
	resumeID := e.Sessions.Get(sessionKey)
	_, lastBackend, _ := e.Sessions.ContextFull(sessionKey)
	backendChanged := lastBackend != "" && lastBackend != tp.Backend

	params := provider.Params{
		Model:           tp.Model,
		Tools:           tp.Tools,
		WriteCapable:    tp.WriteCapable,
		Effort:          tp.Effort,
		MaxTurns:        tp.MaxTurns,
		SystemPrompts:   sysPrompts,
		CacheBreakpoint: cacheBreakpoint,
		ResumeID:      resumeID,
		DataDir:       e.DataDir,
		Env:           e.signalEnv(msg.Env),
	}
	if e.SignalSockPath != "" {
		log.Printf("[comms] signal sock injected: %s (env has %d vars)", e.SignalSockPath, len(params.Env))
	}
	// Inject chain origin so CLI subprocesses can route chain results back.
	params.Env = append(params.Env, fmt.Sprintf("ALF_CHAIN_ORIGIN=%s:%s", channelID.Prefix(), convID))
	if isAPITier {
		params.ResumeID = ""
	}
	if backendChanged {
		log.Printf("[comms] backend switch %s→%s, dropping resume", lastBackend, tp.Backend)
		params.ResumeID = ""
	}

	// Inject conversation history.
	if e.Memory != nil && convID != "" {
		recent, _ := e.Memory.ListMessages(ctx, memory.ConvID(convID), memory.ListOpts{ApplySummary: true})
		convMsgs := memory.BuildContext(recent, memory.DefaultMaxMessages)
		if isAPITier || params.ResumeID == "" {
			if isAPITier {
				// Always flatten to text-only: FlattenForOpenAI collapses multi-turn
				// toolloops into a single assistant message with multiple tool_calls,
				// which is semantically wrong (IDs span different model turns) and
				// causes JSON parse errors on some providers (e.g. SiliconFlow).
				oaiMsgs := memory.FlattenTextOnly(convMsgs)
				ctxMsgs := make([]provider.ContextMessage, len(oaiMsgs))
				for i, m := range oaiMsgs {
					ctxMsgs[i] = provider.ContextMessage{Role: m.Role, Content: m.Content}
				}
				params.ConvMessages = ctxMsgs
			} else {
				if histPrompt := memory.FormatAsSystemPrompt(convMsgs, ctxWeight); histPrompt != "" {
					params.SystemPrompts = append(params.SystemPrompts, histPrompt)
				}
			}
		}
	}

	// Set up streaming.
	rawOnProgress := func(event provider.StreamEvent) {
		switch event.Type {
		case "thinking":
			if event.Text != "" {
				e.emit(channelID, OutEvent{Type: "thinking", Data: map[string]string{"text": event.Text}})
			} else {
				e.emit(channelID, OutEvent{Type: "thinking", Data: map[string]string{}})
			}
		case "tool_use":
			e.emit(channelID, OutEvent{Type: "tool_use", Data: map[string]string{"name": event.Detail}})
		case "tool_input":
			e.emit(channelID, OutEvent{Type: "tool_input", Data: map[string]string{"name": event.Detail, "chunk": event.Text}})
		case "tool_result":
			e.emit(channelID, OutEvent{Type: "tool_result", Data: map[string]string{"tool_id": event.Detail, "result": event.Text}})
		case "text_delta":
			text := stripReactTags(event.Text)
			if text != "" {
				e.emit(channelID, OutEvent{Type: "text_delta", Data: map[string]string{"text": text}})
			}
		}
	}

	var acc *Accumulator
	progressFn := rawOnProgress
	if e.Memory != nil {
		acc = NewAccumulator()
		progressFn = acc.OnProgress(rawOnProgress)
	}

	// Invoke provider.
	// Build the full prompt text (use msg.Text which includes reply context from adapter).
	prompt := msg.Text

	// For API providers, populate Media field from InMessage media entries.
	if isAPITier && len(msg.Media) > 0 {
		for _, m := range msg.Media {
			params.Media = append(params.Media, provider.MediaEntry{
				Type:        m.Type,
				FileName:    m.FileName,
				MimeType:    m.MimeType,
				TempPath:    m.TempPath,
				FramePaths:  m.FramePaths,
				Transcript:  m.Transcript,
				TextContent: m.TextContent,
			})
		}
		log.Printf("[comms] media: populated %d entries for API provider", len(msg.Media))
	}

	invokeSpan := trace.StartSpanFromContext(ctx, "invoke", map[string]string{
		"backend": tp.Backend, "model": tp.Model, "tier": route.Tier,
	})
	if params.ResumeID != "" && invokeSpan != nil {
		invokeSpan.Tag("resume_id", params.ResumeID[:min(len(params.ResumeID), 8)])
	}

	start := time.Now()
	// #340 R4j3: when a Runtime is installed, the happy-path invocation
	// goes through Runtime.ConverseStream. The Provider (possibly wrapped
	// with a ToolLoop) is passed as a per-call Engine override so the
	// wrapping stays in effect. If the Runtime is nil (early-boot, tests
	// without a wired Runtime) we keep the legacy prov.Invoke call. The
	// retry + fallback paths below are intentionally still on the legacy
	// surface — they migrate in R4j4.
	var (
		result *provider.Result
		err    error
	)
	if e.Runtime != nil {
		convReq := buildConverseRequest(prompt, prov, params)
		result, err = e.invokeViaRuntime(ctx, convReq, progressFn)
	} else {
		result, err = prov.Invoke(ctx, prompt, params, progressFn)
	}

	// Retry without resume if session failed (CLI only).
	if err != nil && resumeID != "" && !isAPITier && ctx.Err() == nil {
		log.Printf("[comms] session %s failed (%v), starting fresh", resumeID, err)
		e.Sessions.Archive(sessionKey)
		params.ResumeID = ""
		if e.Memory != nil && convID != "" {
			recent, _ := e.Memory.ListMessages(ctx, memory.ConvID(convID), memory.ListOpts{ApplySummary: true})
			convMsgs := memory.BuildContext(recent, memory.DefaultMaxMessages)
			if histPrompt := memory.FormatAsSystemPrompt(convMsgs, ctxWeight); histPrompt != "" {
				params.SystemPrompts = append(params.SystemPrompts, histPrompt)
			}
		}
		if acc != nil {
			acc = NewAccumulator()
			progressFn = acc.OnProgress(rawOnProgress)
		}
		result, err = prov.Invoke(ctx, prompt, params, progressFn)
	}
	duration := time.Since(start)

	if invokeSpan != nil {
		invokeSpan.Tag("duration_ms", fmt.Sprintf("%d", duration.Milliseconds()))
		if result != nil {
			invokeSpan.Tag("input_tokens", fmt.Sprintf("%d", result.InputTokens))
			invokeSpan.Tag("output_tokens", fmt.Sprintf("%d", result.OutputTokens))
			invokeSpan.Tag("cost_usd", fmt.Sprintf("%.4f", result.CostUSD))
			invokeSpan.Tag("model", result.Model)
		}
		if err != nil {
			invokeSpan.EndWithError(err)
		} else {
			invokeSpan.End()
		}
	}

	// Fallback chain: on any error (except cancellation), try fallback tiers.
	if err != nil && ctx.Err() == nil {
		tiers := e.TierStore.Snapshot()
		fallbackChain := ResolveFallbackChain(route.Tier, tiers)
		for _, fbName := range fallbackChain {
			log.Printf("[comms] tier %q failed (%v), trying fallback → %q", route.Tier, err, fbName)
			e.emit(channelID, OutEvent{Type: "system", Data: map[string]string{
				"text":  fmt.Sprintf("Tier %q failed, trying fallback %q…", route.Tier, fbName),
				"level": "warn",
			}})

			fbTP, found := ResolveTierParams(fbName, tiers, e.DataDir, e.ToolRegistry, e.Registry, e.ResolveModel)
			if !found {
				log.Printf("[comms] fallback %q: tier not found, skipping", fbName)
				continue
			}

			// Pre-flight: skip disabled tiers.
			fbEnabled := false
			for _, ti := range tiers.Tiers {
				if ti.Name == fbName && ti.Enabled {
					fbEnabled = true
					break
				}
			}
			if !fbEnabled {
				log.Printf("[comms] fallback %q: tier disabled, skipping", fbName)
				continue
			}

			// Pre-flight: skip API tiers whose backend is not registered.
			fbIsAPIBackend := fbTP.Backend != "" && fbTP.Backend != "cli"
			if fbIsAPIBackend && !e.Registry.HasBackend(fbTP.Backend) {
				log.Printf("[comms] fallback %q: backend %q not registered, skipping", fbName, fbTP.Backend)
				continue
			}

			fbProv := e.Registry.ForBackend(fbTP.Backend)
			fbIsAPI := fbTP.Backend != "" && fbTP.Backend != "cli"

			// Wrap API provider with tool loop if needed.
			if fbIsAPI && e.ToolRegistry != nil && e.ToolExecutor != nil && len(fbTP.Tools) > 0 {
				e.ToolRegistry.Rescan()
				if apiProv, ok := fbProv.(*provider.APIProvider); ok {
					schemas := e.ToolRegistry.ForToolsStrict(fbTP.Tools)
					if len(schemas) > 0 {
						var tools []map[string]any
						if apiProv.IsDirectOpenAI() {
							tools = tooling.ToOpenAI(schemas)
						} else {
							tools = tooling.ToOpenAICompat(schemas)
						}
						maxT := fbTP.MaxTurns
						if maxT <= 0 {
							maxT = 10
						}
						fbProv = provider.NewToolLoop(apiProv, &toolExecAdapter{exec: e.ToolExecutor, origin: tooling.ChainOrigin{Source: channelID.Prefix(), ConvID: convID}}, tools, maxT)
					}
				}
			}

			fbParams := provider.Params{
				Model:         fbTP.Model,
				Tools:         fbTP.Tools,
				WriteCapable:  fbTP.WriteCapable,
				Effort:        fbTP.Effort,
				MaxTurns:      fbTP.MaxTurns,
				SystemPrompts: params.SystemPrompts,
				DataDir:       e.DataDir,
				Env:           params.Env,
				ConvMessages:  params.ConvMessages,
			}

			if acc != nil {
				acc = NewAccumulator()
				progressFn = acc.OnProgress(rawOnProgress)
			}

			result, err = fbProv.Invoke(ctx, prompt, fbParams, progressFn)
			if err == nil {
				log.Printf("[comms] fallback tier %q succeeded", fbName)
				route.Tier = fbName
				route.Reason += fmt.Sprintf(" [fallback: %s]", fbName)
				tp = fbTP
				prov = fbProv
				isAPITier = fbIsAPI
				e.emit(channelID, OutEvent{Type: "routed", Data: map[string]string{
					"tier": fbName, "model": fbTP.Model,
				}})
				break
			}
			log.Printf("[comms] fallback tier %q also failed: %v", fbName, err)
		}
	}

	if err != nil {
		errMsg := err.Error()
		// User-initiated cancellation: skip error notice — the DELETE handler
		// persists its own "Request was cancelled" message.
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("provider: %w", err)
		}
		notice := classifyProviderError(errMsg, ctx.Err())
		e.emit(channelID, OutEvent{Type: "error", Data: map[string]string{"text": errMsg}})
		e.emit(channelID, OutEvent{Type: "system", Data: map[string]string{
			"text":  notice,
			"level": "error",
		}})
		// Persist error notice to Memory so it survives page reload.
		if e.Memory != nil && convID != "" {
			sysMsg := memory.Message{
				Role:    "system",
				Channel: channel,
				Content: notice,
				Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: notice}},
			}
			_, _ = e.Memory.AppendMessage(ctx, memory.ConvID(convID), sysMsg)
		}
		return nil, fmt.Errorf("provider: %w", err)
	}

	// Compute cost from tokens if not set (API backends).
	if result.CostUSD == 0 && result.InputTokens > 0 && e.BackendConfigs != nil {
		if bc, ok := e.BackendConfigs()[tp.Backend]; ok && (bc.InputPrice > 0 || bc.OutputPrice > 0) {
			result.CostUSD = float64(result.InputTokens)/1e6*bc.InputPrice +
				float64(result.OutputTokens)/1e6*bc.OutputPrice
		}
	}

	sessShort := result.SessionID
	if sessShort == "" && convID != "" {
		sessShort = convID
	}
	if len(sessShort) > 8 {
		sessShort = sessShort[:8]
	}
	log.Printf("[comms] → %s %dms %dt/%dt $%.4f sid:%s", result.Model, duration.Milliseconds(), result.InputTokens, result.OutputTokens, result.CostUSD, sessShort)

	// Update session.
	if result.SessionID != "" {
		e.Sessions.SetWithBackend(sessionKey, result.SessionID, route.Tier, tp.Backend)
	} else if isAPITier {
		e.Sessions.SetWithBackend(sessionKey, convID, route.Tier, tp.Backend)
	}

	// Extract reaction tag and strip any tool-call XML from result text.
	suggestedEmoji, cleanText := ExtractReaction(stripToolXML(result.Text))
	if suggestedEmoji != "" {
		emoji := mood.ValidateOrFallback(suggestedEmoji)
		if emoji != "" {
			e.emit(channelID, OutEvent{Type: "reaction", Data: map[string]string{"emoji": emoji}})
			suggestedEmoji = emoji
		}
	}

	// Persist assistant message (collapsed parallel write into Memory.AppendMessage).
	// Blocks preserve temporal order so the frontend can split them into
	// individual bubbles for display.
	assistantMsgID := string(memory.NewMessageID())
	if e.Memory != nil && convID != "" {
		var blocks []memory.ContentBlock
		if acc != nil {
			blocks = acc.Blocks()
			for i := range blocks {
				if blocks[i].Type == memory.BlockText {
					blocks[i].Text = stripReactTags(blocks[i].Text)
				}
			}
		}
		if len(blocks) == 0 {
			blocks = []memory.ContentBlock{{Type: memory.BlockText, Text: cleanText}}
		}
		asst := memory.Message{
			Role:       "assistant",
			Channel:    channel,
			Content:    cleanText,
			Blocks:     blocks,
			Model:      result.Model,
			Tier:       route.Tier,
			Backend:    tp.Backend,
			CostUSD:    result.CostUSD,
			SessionID:  result.SessionID,
			DurationMs: duration.Milliseconds(),
		}
		if stored, err := e.Memory.AppendMessage(ctx, memory.ConvID(convID), asst); err == nil && stored.ID != "" {
			assistantMsgID = string(stored.ID)
		}
		// Trigger progressive summarization if the conversation has grown.
		// Runs in a background goroutine; does not block the current turn.
		e.maybeSummarizeAsync(ctx, channel, convID)
	}

	// Emit text and done events.
	e.emit(channelID, OutEvent{Type: "text", Data: map[string]string{"text": cleanText}})
	e.emit(channelID, OutEvent{Type: "done", Data: map[string]string{
		"model":      result.Model,
		"tier":       route.Tier,
		"cost":       fmt.Sprintf("%.4f", result.CostUSD),
		"session_id": result.SessionID,
		"duration":   fmt.Sprintf("%d", duration.Milliseconds()),
	}})

	// Turn limit detection: check if the result indicates a turn limit was hit.
	turnLimitHit := false
	if notice := detectTurnLimit(result, cleanText); notice != "" {
		turnLimitHit = true
		resumeHint := "Turn limit reached. Send another message to continue, increase the timeout, or raise the turn limit."
		fullNotice := notice + " " + resumeHint
		e.emit(channelID, OutEvent{Type: "system", Data: map[string]string{
			"text":       fullNotice,
			"level":      "warning",
			"turn_limit": "true",
			"session_id": result.SessionID,
			"tier":       route.Tier,
		}})
		if e.Memory != nil && convID != "" {
			sysMsg := memory.Message{
				Role:    "system",
				Channel: channel,
				Content: fullNotice,
				Blocks:  []memory.ContentBlock{{Type: memory.BlockText, Text: fullNotice}},
			}
			_, _ = e.Memory.AppendMessage(ctx, memory.ConvID(convID), sysMsg)
		}
		log.Printf("[comms] turn limit hit on tier %q (session %s) — resumable", route.Tier, sessShort)
	}

	// Context size warning.
	if _, mc := e.Sessions.Context(sessionKey); mc >= 20 {
		level := "high"
		if mc >= 40 {
			level = "critical"
		}
		e.emit(channelID, OutEvent{Type: "system", Data: map[string]string{
			"text":  fmt.Sprintf("Context is getting large (%d messages). Consider using /new to start fresh.", mc),
			"level": level,
		}})
	}

	// Event log.
	if e.EventLog != nil {
		outText := cleanText
		if len(outText) > 500 {
			outText = outText[:500]
		}
		e.EventLog.Log("message_out", map[string]any{
			"model":       result.Model,
			"cost_usd":    result.CostUSD,
			"text":        outText,
			"text_length": len(cleanText),
			"session_id":  result.SessionID,
			"tier":        route.Tier,
			"channel":     channelID.Prefix(),
			"duration_ms": duration.Milliseconds(),
		})
	}

	// Notify memory extractor of new message.
	if e.OnMessage != nil && result.SessionID != "" {
		e.OnMessage(result.SessionID)
	}

	var resultBlocks []memory.ContentBlock
	if acc != nil {
		for _, b := range acc.Blocks() {
			resultBlocks = append(resultBlocks, memory.ContentBlock{
				Type:   memory.BlockType(string(b.Type)),
				Text:   b.Text,
				Name:   b.Name,
				Input:  b.Input,
				ToolID: b.ToolID,
				Output: b.Output,
			})
		}
	}

	return &ProcessResult{
		Text:           cleanText,
		Model:          result.Model,
		Tier:           route.Tier,
		Reason:         route.Reason,
		CostUSD:        result.CostUSD,
		SessionID:      result.SessionID,
		Reaction:       suggestedEmoji,
		Skills:         e.Sessions.GetSkills(sessionKey),
		Blocks:         resultBlocks,
		Duration:       duration.Milliseconds(),
		UserMsgID:      userMsgID,
		AssistantMsgID: assistantMsgID,
		TurnLimitHit:   turnLimitHit,
	}, nil
}

// ExtractReaction parses a [[react:EMOJI]] marker from the start of text.
func ExtractReaction(text string) (string, string) {
	trimmed := strings.TrimLeft(text, " \n\r\t")
	if !strings.HasPrefix(trimmed, "[[react:") {
		// No leading tag — still strip any mid-text tags.
		cleaned := stripReactTags(text)
		return "", cleaned
	}
	end := strings.Index(trimmed, "]]")
	if end == -1 {
		return "", text
	}
	emoji := trimmed[len("[[react:"):end]
	rest := strings.TrimLeft(trimmed[end+2:], " \n\r\t")
	if emoji == "none" || emoji == "" {
		return "", stripReactTags(rest)
	}
	return emoji, stripReactTags(rest)
}

// stripReactTags removes all remaining [[react:...]] tags from text.
// stripToolXML removes LLM tool-call XML artifacts that some providers
// include in the result text (e.g. <function_calls>, <invoke>, <tool_use>).
var toolXMLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<function_calls>.*?</function_calls>`),
	regexp.MustCompile(`(?s)<invoke>.*?</invoke>`),
	regexp.MustCompile(`(?s)<tool_use>.*?</tool_use>`),
}

func stripToolXML(text string) string {
	for _, re := range toolXMLPatterns {
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

func stripReactTags(text string) string {
	for {
		start := strings.Index(text, "[[react:")
		if start == -1 {
			return text
		}
		end := strings.Index(text[start:], "]]")
		if end == -1 {
			return text
		}
		text = text[:start] + text[start+end+2:]
	}
}

// collectRecentAll merges the last n messages across every conversation in
// the store, sorted chronologically. Replaces conversation.Store.RecentAll.
// Used by the router to give the classifier a shared cross-conv context.
func collectRecentAll(ctx context.Context, s memory.Store, n int) []memory.Message {
	if s == nil || n <= 0 {
		return nil
	}
	convs, err := s.ListConvs(ctx, memory.ConvFilter{})
	if err != nil || len(convs) == 0 {
		return nil
	}
	var all []memory.Message
	for _, c := range convs {
		msgs, err := s.ListMessages(ctx, c.ID, memory.ListOpts{ApplySummary: true})
		if err != nil {
			continue
		}
		all = append(all, msgs...)
	}
	// Sort by CreatedAt ascending; stable for equal timestamps.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j-1].CreatedAt > all[j].CreatedAt; j-- {
			all[j-1], all[j] = all[j], all[j-1]
		}
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// toolExecAdapter bridges tooling.Executor to provider.ToolExecutor.
type toolExecAdapter struct {
	exec   *tooling.Executor
	origin tooling.ChainOrigin // injected into context for fire-and-forget routing
}

func (a *toolExecAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
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
