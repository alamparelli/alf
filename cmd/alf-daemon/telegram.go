package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agents "github.com/alamparelli/alf/internal/runtime/agents"
	"github.com/alamparelli/alf/internal/comms"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/platform/eventlog"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/platform/mood"
	provider "github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/platform/session"
	tgclient "github.com/alamparelli/alf/internal/telegram"
)

// extractReplyContext extracts the full quoted message text from a reply.
func extractReplyContext(msg *Message) string {
	if msg == nil || msg.ReplyToMessage == nil {
		return ""
	}
	return msg.ReplyToMessage.Text
}

// prependReplyContext adds quoted message context to the user's message.
func prependReplyContext(msg *Message) string {
	quoted := extractReplyContext(msg)
	if quoted == "" {
		return msg.Text
	}
	return fmt.Sprintf("[The user is replying to this previous message:\n---\n%s\n---\n]\n%s", quoted, msg.Text)
}

// buildMessageContent builds the complete message content including media captions
func buildMessageContent(msg *Message) string {
	content := msg.Text

	// Include caption for photo/document messages
	if msg.Caption != "" {
		if content != "" {
			content = msg.Caption + "\n" + content
		} else {
			content = msg.Caption
		}
	}

	// Quote-reply without text: provide a meaningful prompt so Claude responds to the quoted content.
	if content == "" && msg.ReplyToMessage != nil {
		quoted := extractReplyContext(msg)
		if quoted != "" {
			return fmt.Sprintf("[The user is replying to this previous message:\n---\n%s\n---\n]\nThe user quoted this message without adding text. Respond to the quoted content.", quoted)
		}
	}

	// Apply reply context if present
	return prependReplyContext(&Message{
		Text:           content,
		ReplyToMessage: msg.ReplyToMessage,
	})
}

// buildRouterMessage builds a short message for the router classifier.
// Includes the user's text with a brief quote hint (not the full quoted text)
// to keep the router prompt small and focused on classification.
func buildRouterMessage(msg *Message) string {
	text := msg.Text
	if msg.Caption != "" {
		if text != "" {
			text = msg.Caption + "\n" + text
		} else {
			text = msg.Caption
		}
	}
	if msg.ReplyToMessage != nil {
		quoted := msg.ReplyToMessage.Text
		if len(quoted) > 100 {
			quoted = quoted[:100] + "..."
		}
		if text == "" {
			return fmt.Sprintf("[Replying to: \"%s\"] (no additional text)", quoted)
		}
		return fmt.Sprintf("[Replying to: \"%s\"]\n%s", quoted, text)
	}
	return text
}

// extFromMime returns a file extension for a MIME type, falling back to the original filename extension.
func extFromMime(mimeType, fileName string) string {
	mimeToExt := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"application/pdf": ".pdf",
		"video/mp4":       ".mp4",
		"video/quicktime": ".mov",
		"video/webm":      ".webm",
		"video/x-matroska": ".mkv",
	}
	if ext, ok := mimeToExt[mimeType]; ok {
		return ext
	}
	if ext := filepath.Ext(fileName); ext != "" {
		return ext
	}
	return ""
}

// hasMedia checks if message contains any media attachments
func hasMedia(msg *Message) bool {
	return len(msg.Photo) > 0 || msg.Document != nil || msg.Video != nil ||
		msg.Animation != nil || msg.Audio != nil || msg.Voice != nil || msg.VideoNote != nil
}

// handleCommand processes known /commands. Returns true if handled.
func handleCommand(tg *tgclient.Client, msg *Message, engine *comms.ChatEngine, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool, orch *agents.Orchestrator) bool {
	cmd := strings.SplitN(msg.Text, " ", 2)[0]
	channelID := comms.ChannelID(fmt.Sprintf("tg:%d", msg.Chat.ID))
	switch cmd {
	case "/login":
		handleLogin(tg, msg, magic, ccExternalURL, allowedChatIDs)
		return true
	case "/new", "/clear":
		old := engine.NewSession(channelID, false)
		reply := "New session started."
		if old != "" {
			reply = "Previous session archived. New session started."
		}
		tg.SendHTML(msg.Chat.ID, reply)
		return true
	case "/resume":
		newText, isResume, errMsg := engine.HandleResume(channelID, msg.Text)
		if errMsg != "" {
			tg.SendHTML(msg.Chat.ID, errMsg)
			return true
		}
		if isResume {
			msg.Text = newText
			return false // fall through to normal processing
		}
		return true
	case "/start":
		engine.NewSession(channelID, true)
		// Auto-trigger onboarding conversation - fall through to normal message processing.
		msg.Text = "hello"
		return false
	case "/restart":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		tg.SendHTML(msg.Chat.ID, "Restarting ALF daemon...")
		log.Println("restart requested via /restart command")
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(0)
		}()
		return true
	case "/cancel":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		running := orch.Running()
		if len(running) == 0 {
			tg.SendHTML(msg.Chat.ID, "No agent jobs running.")
			return true
		}
		n := orch.CancelAll()
		tg.SendHTML(msg.Chat.ID, fmt.Sprintf("Cancelled %d agent job(s).", n))
		return true
	case "/jobs":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		running := orch.Running()
		if len(running) == 0 {
			tg.SendHTML(msg.Chat.ID, "No agent jobs running.")
			return true
		}
		var lines []string
		for _, rt := range running {
			elapsed := time.Since(rt.StartedAt).Truncate(time.Second)
			iter := 0
			if rt.Meta != nil {
				iter = rt.Meta.Iterations
			}
			lines = append(lines, fmt.Sprintf("• <code>%s</code> - %s, iteration %d", rt.ID, elapsed, iter))
		}
		tg.SendHTML(msg.Chat.ID, "<b>Running agent jobs:</b>\n"+strings.Join(lines, "\n"))
		return true
	case "/skills":
		parts := strings.SplitN(msg.Text, " ", 2)
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		sessionKey := channelID.SessionKey()
		if arg == "clear" || arg == "reset" {
			engine.Sessions.ClearSkills(sessionKey)
			tg.SendHTML(msg.Chat.ID, "Active skills cleared from session.")
		} else {
			active := engine.Sessions.GetSkills(sessionKey)
			if len(active) == 0 {
				tg.SendHTML(msg.Chat.ID, "No skills active in this session.\n\nUse <code>/skills clear</code> to reset.")
			} else {
				var lines []string
				for _, s := range active {
					lines = append(lines, "• "+s)
				}
				tg.SendHTML(msg.Chat.ID, "<b>Active skills:</b>\n"+strings.Join(lines, "\n")+"\n\nUse <code>/skills clear</code> to reset.")
			}
		}
		return true
	case "/help":
		help := "<b>Available commands:</b>\n" +
			"/help - Show this message\n" +
			"/new - Start a new conversation session\n" +
			"/clear - Clear and start a new session\n" +
			"/resume - Continue after a turn limit hit\n" +
			"/skills - List active skills (/skills clear to reset)\n" +
			"/tool - Manage quarantined tools (keep/revert)\n" +
			"/bash - Execute a bash command directly\n" +
			"/jobs - List running agent jobs\n" +
			"/cancel - Cancel all running agent jobs\n" +
			"/restart - Restart the ALF daemon\n" +
			"/login - Get a login link for the Control Center\n" +
			"/start - Re-run onboarding (get to know each other)"
		tg.SendHTML(msg.Chat.ID, help)
		return true
	case "/tool":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		parts := strings.SplitN(msg.Text, " ", 2)
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		response := cmdToolTG(engine, channelID, arg)
		tg.SendHTML(msg.Chat.ID, response)
		return true
	case "/bash":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		parts := strings.SplitN(msg.Text, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			tg.SendHTML(msg.Chat.ID, "Usage: <code>/bash &lt;command&gt;</code>")
			return true
		}
		go execBashCommand(tg, msg.Chat.ID, strings.TrimSpace(parts[1]))
		return true
	}
	return false
}

// cmdToolTG handles /tool for Telegram (delegates to integrity guard).
func cmdToolTG(engine *comms.ChatEngine, channelID comms.ChannelID, args string) string {
	if engine.ToolExecutor == nil || engine.ToolExecutor.Integrity == nil {
		return "Tool integrity guard is not enabled."
	}
	ig := engine.ToolExecutor.Integrity

	parts := strings.Fields(args)
	if len(parts) == 0 {
		quarantined := ig.Quarantined()
		if len(quarantined) == 0 {
			return "No quarantined tools."
		}
		var lines []string
		for _, qt := range quarantined {
			lines = append(lines, fmt.Sprintf("• <b>%s</b> (old: %s, new: %s)", qt.Name, qt.OldHash[:12], qt.NewHash[:12]))
		}
		return "<b>Quarantined tools:</b>\n" + strings.Join(lines, "\n") + "\n\nUse <code>/tool keep &lt;name&gt;</code> or <code>/tool revert &lt;name&gt;</code>"
	}

	if len(parts) < 2 {
		return "Usage: <code>/tool keep &lt;name&gt;</code> | <code>/tool revert &lt;name&gt;</code>"
	}

	action, name := parts[0], parts[1]
	switch action {
	case "keep":
		if err := ig.ApproveModified(name); err != nil {
			return fmt.Sprintf("Failed: %v", err)
		}
		engine.ToolRegistry.Rescan()
		return fmt.Sprintf("Tool <b>%s</b> approved. Modified version is now active.", name)
	case "revert":
		if err := ig.RevertTool(name); err != nil {
			return fmt.Sprintf("Failed: %v", err)
		}
		return fmt.Sprintf("Tool <b>%s</b> reverted to original.", name)
	default:
		return "Usage: <code>/tool keep &lt;name&gt;</code> | <code>/tool revert &lt;name&gt;</code>"
	}
}

func handleLogin(tg *tgclient.Client, msg *Message, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) {
	chatID := msg.Chat.ID

	if len(allowedChatIDs) == 0 {
		tg.SendHTML(chatID, "Login is not configured. Set ALLOWED_CHAT_IDS to enable it.")
		return
	}

	if !allowedChatIDs[chatID] {
		tg.SendHTML(chatID, "You are not authorized to access the Control Center.")
		return
	}

	// Send inline keyboard with session duration options.
	keyboard := map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "24 hours", "callback_data": "login:24h"},
				{"text": "7 days", "callback_data": "login:7d"},
				{"text": "30 days", "callback_data": "login:30d"},
			},
		},
	}
	tg.SendKeyboard(chatID, "Choose session duration:", keyboard)
}

func handleCallbackQuery(tg *tgclient.Client, client *http.Client, token string, cb *CallbackQuery, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) {
	// Always answer callback to remove the loading indicator.
	defer answerCallbackQuery(client, token, cb.ID)

	if cb.Message == nil {
		return
	}

	chatID := cb.Message.Chat.ID

	if !strings.HasPrefix(cb.Data, "login:") {
		return
	}

	if !allowedChatIDs[chatID] {
		tg.SendHTML(chatID, "You are not authorized to access the Control Center.")
		return
	}

	var ttl time.Duration
	var label string
	switch cb.Data {
	case "login:24h":
		ttl = 24 * time.Hour
		label = "24 hours"
	case "login:7d":
		ttl = 7 * 24 * time.Hour
		label = "7 days"
	case "login:30d":
		ttl = 30 * 24 * time.Hour
		label = "30 days"
	default:
		tg.SendHTML(chatID, "Unknown duration. Send /login to try again.")
		return
	}

	code, err := magic.Issue(chatID, ttl)
	if err != nil {
		log.Printf("magic issue error: %v", err)
		tg.SendHTML(chatID, "Failed to generate login link. Try again.")
		return
	}

	link := fmt.Sprintf("%s/auth?code=%s", strings.TrimRight(ccExternalURL, "/"), code)
	tg.SendHTMLNoPreview(chatID, fmt.Sprintf("Session: %s · Expires in 5 min\n%s", label, link))
}

func answerCallbackQuery(client *http.Client, token string, callbackID string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	payload, _ := json.Marshal(map[string]any{
		"callback_query_id": callbackID,
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("answerCallbackQuery error: %v", err)
		return
	}
	defer resp.Body.Close()
}

// typingIndicator sends periodic Telegram chat actions (typing, choose_sticker, etc.)
// without sending or editing any messages.
type typingIndicator struct {
	tg     *tgclient.Client
	chatID int64
	action string
	mu     sync.Mutex
	done   chan struct{}
}

func newTypingIndicator(tg *tgclient.Client, chatID int64, action string) *typingIndicator {
	ti := &typingIndicator{
		tg:     tg,
		chatID: chatID,
		action: action,
		done:   make(chan struct{}),
	}
	go ti.run()
	return ti
}

func (ti *typingIndicator) run() {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ti.done:
			return
		case <-ticker.C:
			ti.mu.Lock()
			action := ti.action
			ti.mu.Unlock()
			ti.tg.SendChatAction(ti.chatID, action)
		}
	}
}

// SetAction changes the chat action type.
func (ti *typingIndicator) SetAction(action string) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.action = action
}

// Stop halts the typing indicator.
func (ti *typingIndicator) Stop() {
	select {
	case <-ti.done:
	default:
		close(ti.done)
	}
}

// maybeSpontaneousReact validates an emoji (with fallback), applies mood-gate probability,
// and sends the reaction. Runs synchronously so the reaction lands before the reply.
func maybeSpontaneousReact(tg *tgclient.Client, chatID, msgID int64, emoji, contextDir string) {
	emoji = mood.ValidateOrFallback(emoji)
	if emoji == "" {
		return
	}
	state := mood.GetCurrentState(contextDir)
	if !mood.ShouldReact(state) {
		log.Printf("reaction %s suggested but skipped (state=%s)", emoji, state)
		return
	}
	log.Printf("→ spontaneous reaction %s on msg %d (state=%s)", emoji, msgID, state)
	tg.SetMessageReaction(chatID, msgID, emoji)
}

// extractReaction parses a [[react:EMOJI]] marker from the start of text.
// Returns the emoji (or "") and the cleaned text with the marker stripped.
func extractReaction(text string) (string, string) {
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

// handleReaction processes an emoji reaction on an Alf message.
func handleReaction(tg *tgclient.Client, chatID, messageID int64, emoji, contextDir, dataDir string, chatSessions *session.Store, tierStore cc.TierStore, alfMsgIDs *ringBuffer, eventLog *eventlog.Logger, prov *provider.CLIProvider, memStore memory.Store, engine *comms.ChatEngine) {
	// Log the reaction and update live feedback.
	mood.LogReaction(dataDir, emoji, messageID)
	mood.UpdateLiveFeedback(contextDir, dataDir)

	score, state := mood.GetTodayScore(dataDir)
	log.Printf("reaction scored: emoji=%s score=%d state=%s", emoji, score, state)

	// Mirror reaction.
	shouldReact := mood.ShouldReact(state)
	log.Printf("reaction decision: should_react=%v (state=%s)", shouldReact, state)
	if shouldReact {
		mirror := mood.ChooseMirror(emoji, state)
		log.Printf("reaction mirror: %s → %s (state=%s)", emoji, mirror, state)
		if mirror != "" {
			// Human-like delay before mirror reacting (1.5–4.5s).
			delay := time.Duration(1500+rand.Intn(3000)) * time.Millisecond
			time.Sleep(delay)

			if err := tg.SetMessageReaction(chatID, messageID, mirror); err != nil {
				log.Printf("mirror reaction error: %v", err)
			} else {
				log.Printf("→ mirror reaction sent: %s on msg %d", mirror, messageID)
			}
		}
	}

	// Extract preference learning via comms engine.
	if engine != nil {
		go engine.ExtractReactionLearning(emoji, comms.ChannelID("tg:"+fmt.Sprint(chatID)))
	}

	// Negative reaction follow-up: ask what went wrong.
	if !mood.IsNegative(emoji) {
		return
	}

	// Strong negative → always follow up. Mild negative → 50% chance.
	if !mood.IsStrongNegative(emoji) && rand.Float64() > 0.5 {
		log.Printf("mild negative %s - skipping follow-up (coin flip)", emoji)
		return
	}

	log.Printf("negative reaction %s - triggering follow-up", emoji)

	// Small delay so mirror reaction lands first.
	time.Sleep(2 * time.Second)
	tg.SendChatAction(chatID, "typing")

	var prompt string
	langNote := "IMPORTANT: Reply in the same language the user has been using in this conversation."
	if mood.IsStrongNegative(emoji) {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (strong negative). Something is clearly wrong. Acknowledge the negative feedback briefly, identify what likely went wrong in your previous response, and ask a short direct question to understand what they expected. Keep it to 2-3 sentences max. Don't be defensive. %s", emoji, langNote)
	} else {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (mild negative). Briefly acknowledge the feedback and ask a short question to understand what could be improved. One or two sentences max. Stay casual. %s", emoji, langNote)
	}

	resumeID := chatSessions.Get(chatID)
	// Use the cheapest tier for fast follow-up. Resolve from user config
	// via firstFallbackTier → DefaultFallbackModel; never hardcode a model.
	model := ""
	fallback := firstFallbackTier(tierStore)
	for _, t := range tierStore.Current().Tiers {
		if t.Name == fallback {
			if m := resolveModel(t.Model); m != "" {
				model = m
			} else if t.Model != "" {
				model = t.Model // preserve non-Claude models (e.g. gpt-5.4)
			}
			break
		}
	}
	if model == "" {
		model = cc.DefaultFallbackModel(tierStore.Current())
	}
	if model == "" {
		log.Printf("telegram: no fallback model configured, skipping negative-reaction follow-up")
		return
	}

	result, err := prov.Invoke(context.Background(), prompt, provider.Params{
		Model:    model,
		ResumeID: resumeID,
		DataDir:  dataDir,
	}, nil)
	if err != nil {
		log.Printf("negative follow-up error: %v", err)
		return
	}

	if result.SessionID != "" {
		chatSessions.SetWithContext(chatID, result.SessionID, "follow-up")
	}

	eventLog.Log("negative_followup", map[string]any{
		"chat_id": chatID,
		"emoji":   emoji,
		"model":   result.Model,
	})

	if result.Text == "Done (no text output)." {
		return
	}
	if msgID, err := tg.SendMessageReturnID(chatID, result.Text); err == nil && msgID != 0 {
		alfMsgIDs.Add(msgID)
	}
}

// execBashCommand runs a bash command and sends the output via Telegram.
func execBashCommand(tg *tgclient.Client, chatID int64, command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	tg.SendChatAction(chatID, "typing")

	err := cmd.Run()
	result := out.String()
	if len(result) > 4000 {
		result = result[:4000] + "\n... (truncated)"
	}

	var msg string
	if err != nil {
		if result != "" {
			msg = fmt.Sprintf("<pre>%s</pre>\n\nExit: %v", tgclient.EscapeHTML(result), err)
		} else {
			msg = fmt.Sprintf("Error: %v", err)
		}
	} else if result == "" {
		msg = "<i>Command completed (no output)</i>"
	} else {
		msg = fmt.Sprintf("<pre>%s</pre>", tgclient.EscapeHTML(result))
	}

	tg.SendHTML(chatID, msg)
}
