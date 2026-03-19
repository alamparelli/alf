package comms

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/mood"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
)

// Process handles an incoming message through the full pipeline.
// The adapter is responsible for calling this method and translating OutEvents
// into platform-specific output (SSE, Telegram messages, etc.).
func (e *ChatEngine) Process(ctx context.Context, msg InMessage) (*ProcessResult, error) {
	channelID := msg.ChannelID
	channel := channelID.ConvChannel()
	sessionKey := channelID.SessionKey()

	// 1. Persist user message to ConvStore.
	var convID string
	if e.ConvStore != nil {
		convID = e.ConvStore.ConvID(channel)
		e.ConvStore.Append(conversation.Message{
			ID:        conversation.NewMessageID(),
			ConvID:    convID,
			Channel:   channel,
			Role:      "user",
			Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: msg.DisplayText()}},
			Timestamp: time.Now(),
		})
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
	forcedTier := msg.ForcedTier
	if forcedTier == "" {
		if ft := e.Sessions.GetForcedTier(sessionKey); ft != "" {
			forcedTier = ft
		}
	}

	// 3. Pre-route memory recall.
	recall := Recall(e.Recaller, msg.Text)

	// 4. Classify message.
	lastTier, msgCount := e.Sessions.Context(sessionKey)

	var recentCtx string
	if e.ConvStore != nil {
		recentCtx = conversation.BuildRouterContext(e.ConvStore.RecentAll(6), 3)
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
		e.Sessions.TouchContext(sessionKey, "router")
		// Persist to ConvStore.
		if e.ConvStore != nil {
			e.ConvStore.Append(conversation.Message{
				ID:        conversation.NewMessageID(),
				ConvID:    convID,
				Channel:   channel,
				Role:      "assistant",
				Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: route.Response}},
				Timestamp: time.Now(),
				Model:     "router",
				Tier:      "router",
			})
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
			Text:     route.Response,
			Model:    "router",
			Tier:     "router",
			Reason:   route.Reason,
			Reaction: route.React,
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

	e.emit(channelID, OutEvent{Type: "routed", Data: map[string]string{"tier": route.Tier, "model": tp.Model}})

	if e.EventLog != nil {
		e.EventLog.Log("router_classify", map[string]any{
			"tier":    route.Tier,
			"reason":  route.Reason,
			"model":   tp.Model,
			"channel": channelID.Prefix(),
		})
	}

	// 9. Agent dispatch.
	if route.Tier == "agent" && e.Orchestrator != nil {
		return e.processAgent(ctx, msg, tp, recall, convID)
	}

	// 10-13. Standard provider invocation.
	return e.processStandard(ctx, msg, tp, route, recall, convID)
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
func (e *ChatEngine) processAgent(ctx context.Context, msg InMessage, tp TierParams, recall RecallResult, convID string) (*ProcessResult, error) {
	channelID := msg.ChannelID
	channel := channelID.ConvChannel()

	var convCtx string
	if e.ConvStore != nil {
		if msgs := e.ConvStore.Recent(channel, 0); len(msgs) > 0 {
			convCtx = conversation.BuildRouterContext(msgs, 5)
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

	start := time.Now()
	orchResult, orchMeta, orchErr := e.Orchestrator.Run(ctx, msg.Text, orchPrep.SystemPrompts, orchPrep.Config, onProgress)
	duration := time.Since(start)

	if orchErr != nil {
		e.emit(channelID, OutEvent{Type: "error", Data: map[string]string{"text": orchErr.Error()}})
		return nil, fmt.Errorf("agent: %w", orchErr)
	}

	// Persist agent response.
	if e.ConvStore != nil {
		e.ConvStore.Append(conversation.Message{
			ID:        conversation.NewMessageID(),
			ConvID:    convID,
			Channel:   channel,
			Role:      "assistant",
			Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: orchResult}},
			Timestamp: time.Now(),
			Model:     "agent",
			Tier:      "agent",
			CostUSD:   orchMeta.TotalCost,
		})
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
		Text:     orchResult,
		Model:    "agent",
		Tier:     "agent",
		CostUSD:  orchMeta.TotalCost,
		IsAgent:  true,
		Duration: duration.Milliseconds(),
	}, nil
}

// processStandard handles non-agent tier invocation (CLI or API provider).
func (e *ChatEngine) processStandard(ctx context.Context, msg InMessage, tp TierParams, route RouteResult, recall RecallResult, convID string) (*ProcessResult, error) {
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
	// Memory recall.
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
	// Tool reminder.
	if ctxWeight != "light" {
		if reminder := memory.ToolReminder(e.ContextDir); reminder != "" {
			sysPrompts = append(sysPrompts, reminder)
		}
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
				tools := tooling.ToOpenAI(schemas)
				maxTurns := tp.MaxTurns
				if maxTurns <= 0 {
					maxTurns = 10
				}
				prov = provider.NewToolLoop(apiProv, &toolExecAdapter{exec: e.ToolExecutor}, tools, maxTurns)
				log.Printf("[comms] tool loop enabled: %d tools, max_turns=%d", len(schemas), maxTurns)
				toolNames := make([]string, len(schemas))
				for i, s := range schemas {
					toolNames[i] = s.Name
				}
				sysPrompts = append([]string{memory.ToolInstruction(toolNames)}, sysPrompts...)
			}
		}
	}

	// Resolve resume ID.
	resumeID := e.Sessions.Get(sessionKey)
	_, lastBackend, _ := e.Sessions.ContextFull(sessionKey)
	backendChanged := lastBackend != "" && lastBackend != tp.Backend

	params := provider.Params{
		Model:         tp.Model,
		Tools:         tp.Tools,
		WriteCapable:  tp.WriteCapable,
		Effort:        tp.Effort,
		MaxTurns:      tp.MaxTurns,
		SystemPrompts: sysPrompts,
		ResumeID:      resumeID,
		DataDir:       e.DataDir,
		Env:           e.signalEnv(msg.Env),
	}
	if e.SignalSockPath != "" {
		log.Printf("[comms] signal sock injected: %s (env has %d vars)", e.SignalSockPath, len(params.Env))
	}
	if isAPITier {
		params.ResumeID = ""
	}
	if backendChanged {
		log.Printf("[comms] backend switch %s→%s, dropping resume", lastBackend, tp.Backend)
		params.ResumeID = ""
	}

	// Inject conversation history.
	if e.ConvStore != nil {
		convMsgs := conversation.BuildContext(e.ConvStore.Recent(channel, 0), conversation.DefaultMaxMessages)
		if isAPITier || params.ResumeID == "" {
			if isAPITier {
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
				if histPrompt := conversation.FormatAsSystemPrompt(convMsgs, ctxWeight); histPrompt != "" {
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
			e.emit(channelID, OutEvent{Type: "text_delta", Data: map[string]string{"text": event.Text}})
		}
	}

	var acc *conversation.Accumulator
	progressFn := rawOnProgress
	if e.ConvStore != nil {
		acc = conversation.NewAccumulator()
		progressFn = acc.OnProgress(rawOnProgress)
	}

	// Invoke provider.
	// Build the full prompt text (use msg.Text which includes reply context from adapter).
	prompt := msg.Text

	start := time.Now()
	result, err := prov.Invoke(ctx, prompt, params, progressFn)

	// Retry without resume if session failed (CLI only).
	if err != nil && resumeID != "" && !isAPITier {
		log.Printf("[comms] session %s failed (%v), starting fresh", resumeID, err)
		e.Sessions.Archive(sessionKey)
		params.ResumeID = ""
		if e.ConvStore != nil {
			convMsgs := conversation.BuildContext(e.ConvStore.Recent(channel, 0), conversation.DefaultMaxMessages)
			if histPrompt := conversation.FormatAsSystemPrompt(convMsgs, ctxWeight); histPrompt != "" {
				params.SystemPrompts = append(params.SystemPrompts, histPrompt)
			}
		}
		if acc != nil {
			acc = conversation.NewAccumulator()
			progressFn = acc.OnProgress(rawOnProgress)
		}
		result, err = prov.Invoke(ctx, prompt, params, progressFn)
	}
	duration := time.Since(start)

	if err != nil {
		e.emit(channelID, OutEvent{Type: "error", Data: map[string]string{"text": err.Error()}})
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
	if len(sessShort) > 8 {
		sessShort = sessShort[:8]
	}
	log.Printf("[comms] → %s %dms %dt/%dt $%.4f sid:%s", result.Model, duration.Milliseconds(), result.InputTokens, result.OutputTokens, result.CostUSD, sessShort)

	// Update session.
	if result.SessionID != "" {
		e.Sessions.SetWithBackend(sessionKey, result.SessionID, route.Tier, tp.Backend)
	} else if isAPITier {
		e.Sessions.TouchContext(sessionKey, route.Tier)
	}

	// Extract reaction tag.
	suggestedEmoji, cleanText := ExtractReaction(result.Text)
	if suggestedEmoji != "" {
		emoji := mood.ValidateOrFallback(suggestedEmoji)
		if emoji != "" {
			e.emit(channelID, OutEvent{Type: "reaction", Data: map[string]string{"emoji": emoji}})
			suggestedEmoji = emoji
		}
	}

	// Persist assistant message.
	if e.ConvStore != nil {
		var blocks []conversation.ContentBlock
		if acc != nil {
			blocks = acc.Blocks()
		}
		if len(blocks) == 0 {
			blocks = []conversation.ContentBlock{{Type: conversation.BlockText, Text: cleanText}}
		}
		e.ConvStore.Append(conversation.Message{
			ID:        conversation.NewMessageID(),
			ConvID:    convID,
			Channel:   channel,
			Role:      "assistant",
			Blocks:    blocks,
			Timestamp: time.Now(),
			Model:     result.Model,
			Tier:      route.Tier,
			Backend:   tp.Backend,
			CostUSD:   result.CostUSD,
			SessionID: result.SessionID,
		})
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

	var resultBlocks []conversation.ContentBlock
	if acc != nil {
		resultBlocks = acc.Blocks()
	}

	return &ProcessResult{
		Text:      cleanText,
		Model:     result.Model,
		Tier:      route.Tier,
		Reason:    route.Reason,
		CostUSD:   result.CostUSD,
		SessionID: result.SessionID,
		Reaction:  suggestedEmoji,
		Skills:    e.Sessions.GetSkills(sessionKey),
		Blocks:    resultBlocks,
		Duration:  duration.Milliseconds(),
	}, nil
}

// ExtractReaction parses a [[react:EMOJI]] marker from the start of text.
func ExtractReaction(text string) (string, string) {
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

// toolExecAdapter bridges tooling.Executor to provider.ToolExecutor.
type toolExecAdapter struct {
	exec *tooling.Executor
}

func (a *toolExecAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
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
