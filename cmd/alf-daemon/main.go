package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/gittrack"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/router"
	"github.com/alamparelli/alf/internal/session"
	tgclient "github.com/alamparelli/alf/internal/telegram"
	"github.com/alamparelli/alf/internal/tierfs"
	"github.com/alamparelli/alf/internal/updater"
)

var version = "dev"

func main() {
	token := readSecret("TELEGRAM_BOT_TOKEN")
	chatID := readSecret("TELEGRAM_CHAT_ID")
	authToken := readSecret("CC_AUTH_TOKEN")

	if token == "" || chatID == "" {
		// Log diagnostic info to help users debug secrets issues.
		log.Println("ERROR: TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
		for _, name := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID"} {
			filePath := os.Getenv(name + "_FILE")
			if filePath == "" {
				log.Printf("  %s_FILE env var: not set", name)
			} else if _, err := os.Stat(filePath); err != nil {
				log.Printf("  %s_FILE=%s: file not found", name, filePath)
			} else {
				data, _ := os.ReadFile(filePath)
				if strings.TrimSpace(string(data)) == "" {
					log.Printf("  %s_FILE=%s: file exists but is empty", name, filePath)
				} else {
					log.Printf("  %s_FILE=%s: file exists with content", name, filePath)
				}
			}
			if v := os.Getenv(name); v != "" {
				log.Printf("  %s env var: set", name)
			} else {
				log.Printf("  %s env var: not set", name)
			}
		}
		log.Fatal("Exiting. Ensure secrets are configured — see https://github.com/alamparelli/alf#secrets")
	}

	// Verify claude CLI is available.
	if _, err := exec.LookPath("claude"); err != nil {
		log.Fatal("claude CLI not found in PATH")
	}

	// Data directory for logs, sessions, memories, etc.
	dataDir := "/home/node/data"
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}

	// Config directory (RW for CC, separate from data volume).
	configDir := "/opt/alf/config"
	if d := os.Getenv("ALF_CONFIG_DIR"); d != "" {
		configDir = d
	}

	// Parse allowed chat IDs for login authorization.
	// Default to TELEGRAM_CHAT_ID if ALLOWED_CHAT_IDS not explicitly set.
	allowedRaw := readSecret("ALLOWED_CHAT_IDS")
	if allowedRaw == "" {
		allowedRaw = chatID
	}
	allowedChatIDs := parseAllowedChatIDs(allowedRaw)

	// Shared stats for CC status endpoint.
	stats := cc.NewStats()

	// Reload channel: CC writes, daemon reads.
	reloadCh := make(chan cc.ReloadEvent, 4)

	// Magic link auth stores (shared between CC and daemon).
	magic := cc.NewMagicStore(nil)
	magic.StartCleanup()
	sessions := cc.NewSessionStore(nil)
	sessions.StartCleanup()

	// CC external URL for magic link generation.
	ccExternalURL := os.Getenv("CC_EXTERNAL_URL")
	if ccExternalURL == "" {
		ccExternalURL = "http://localhost:8080"
	}

	log.Printf("alf-daemon %s starting...", version)

	// Write version file so Claude -p can read it.
	os.WriteFile(filepath.Join(dataDir, ".version"), []byte(version), 0o644)

	// Ensure directories exist.
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(filepath.Join(dataDir, "logs", "events"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755)
	for _, sub := range []string{"tools", "skills", "memories"} {
		os.MkdirAll(filepath.Join(dataDir, sub), 0o755)
	}

	// Migrate config from old data/config/ to configDir (before loading).
	migrateConfig(dataDir, configDir)

	// Load initial config.
	configStore := cc.NewFileConfigStore(cc.ConfigPath(configDir))
	cfg, err := configStore.Load()
	if err != nil {
		log.Printf("warning: failed to load config: %v", err)
		cfg = cc.DefaultConfig()
	}
	// Load initial tiers config.
	tierStore := cc.NewFileTierStore(cc.TiersPath(configDir))
	if err := tierStore.Reload(); err != nil {
		log.Printf("warning: failed to load tiers: %v", err)
	}
	migrateConfig(dataDir, configDir)

	// Bootstrap default memory files (soul.md, mood.md, index.md).
	memory.Bootstrap(filepath.Join(dataDir, "memories"))

	// TierFS for per-tier system prompts and skills (inside configDir).
	tierFS := tierfs.New(configDir)
	for _, t := range tierStore.Current().Tiers {
		if err := tierFS.EnsureDir(t.Name); err != nil {
			log.Printf("warning: failed to create tier dir %q: %v", t.Name, err)
		}
	}

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		server, err := cc.New(dataDir, configDir, stats, version, authToken, reloadCh, magic, sessions, tierFS)
		if err != nil {
			log.Printf("warning: failed to start Control Center: %v", err)
		} else {
			go func() {
				if err := server.Start(); err != nil && err != http.ErrServerClosed {
					log.Printf("Control Center error: %v", err)
				}
			}()
			log.Printf("Control Center started on :8080 (allowed_chat_ids=%d, external_url=%s)", len(allowedChatIDs), ccExternalURL)
		}
	} else {
		log.Println("CC_AUTH_TOKEN and ALLOWED_CHAT_IDS not set — Control Center disabled")
	}

	// Session store for Claude --resume support.
	sessionTimeout := time.Duration(cfg.SessionTimeout) * time.Minute
	if sessionTimeout <= 0 {
		sessionTimeout = 30 * time.Minute
	}
	chatSessions := session.New(dataDir, sessionTimeout)

	// JSONL event logger.
	eventLog := eventlog.New(dataDir)
	defer eventLog.Close()

	// Git tracker for data directory version history.
	var git *gittrack.Tracker
	if cfg.GitTrack {
		git = gittrack.New(dataDir)
		if err := git.Init(); err != nil {
			log.Printf("warning: git tracker init failed: %v", err)
			git = nil
		} else {
			if cfg.GitSweepInterval > 0 {
				git.SetInterval(time.Duration(cfg.GitSweepInterval) * time.Minute)
				git.StartSweep()
			}
			defer git.Stop()
			log.Printf("git tracker started (sweep=%dm)", cfg.GitSweepInterval)
		}
	}

	var offset int64
	client := &http.Client{Timeout: 35 * time.Second}

	// Telegram client for sending formatted messages.
	tg := tgclient.NewClient(token)
	tg.HTTP = client

	// Auto-update checker.
	if cfg.AutoUpdateCheck {
		image := os.Getenv("ALF_IMAGE")
		if image == "" {
			image = "ghcr.io/alamparelli/alf"
		}
		notifyFn := func(current, latest string) {
			log.Printf("update available: %s → %s", current, latest)
			if cfg.AutoUpdateNotify && token != "" && chatID != "" {
				cid, _ := strconv.ParseInt(chatID, 10, 64)
				if cid != 0 {
					tg.SendHTML(cid, fmt.Sprintf("Update available: %s → %s\nRun <code>alf upgrade</code> on the host to update.", current, latest))
				}
			}
		}
		updateInterval := time.Duration(cfg.AutoUpdateCheckInterval) * time.Second
		if updateInterval <= 0 {
			updateInterval = 21600 * time.Second
		}
		uc := updater.New(image, version, updateInterval, notifyFn)
		uc.Start()
		defer uc.Stop()
	}

	for {
		// Check for reload events (non-blocking).
		select {
		case event := <-reloadCh:
			switch event {
			case cc.ReloadConfig:
				if newCfg, err := configStore.Load(); err == nil {
					cfg = newCfg
					if cfg.SessionTimeout > 0 {
						chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					}
					log.Printf("config reloaded: log_level=%s session_timeout=%dm", cfg.LogLevel, cfg.SessionTimeout)
				}
				if git != nil {
					git.Commit("config updated via CC")
				}
			case cc.ReloadTiers:
				log.Println("tiers reloaded")
				if git != nil {
					git.Commit("tiers updated via CC")
				}
			case cc.ReloadTools:
				log.Println("tools reloaded")
				if git != nil {
					git.Commit("tools updated via CC")
				}
			case cc.ReloadSkills:
				log.Println("skills reloaded")
				if git != nil {
					git.Commit("skills updated via CC")
				}
			case cc.ReloadTierFiles:
				log.Println("tier files reloaded")
				if git != nil {
					git.Commit("tier files updated via CC")
				}
			}
		default:
		}

		updates, err := getUpdates(client, token, offset)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1

			// Handle callback queries (inline keyboard button presses).
			if u.CallbackQuery != nil {
				handleCallbackQuery(tg, client, token, u.CallbackQuery, magic, ccExternalURL, allowedChatIDs)
				continue
			}

			if u.Message == nil || u.Message.Text == "" {
				continue
			}

			log.Printf("← %s: %s", u.Message.From.Username, u.Message.Text)
			stats.RecordMessage()

			truncated := u.Message.Text
			if len(truncated) > 200 {
				truncated = truncated[:200]
			}
			eventLog.Log("message_in", map[string]any{
				"chat_id":  u.Message.Chat.ID,
				"username": u.Message.From.Username,
				"text":     truncated,
			})

			// Command routing: handle /commands before passing to Claude.
			if strings.HasPrefix(u.Message.Text, "/") {
				if handleCommand(tg, u.Message, chatSessions, eventLog, magic, ccExternalURL, allowedChatIDs) {
					continue
				}
				// Unknown /commands fall through to Claude.
			}

			chatID := u.Message.Chat.ID
			resumeID := chatSessions.Get(chatID)

			// Get conversation context for routing.
			lastTier, msgCount := chatSessions.Context(chatID)

			// Route message to appropriate tier.
			var tp tierParams
			routeResult := router.Classify(router.ClassifyInput{
				Message:      u.Message.Text,
				Tiers:        tierStore.Current(),
				DataDir:      dataDir,
				ConfigDir:    configDir,
				LastTier:     lastTier,
				MessageCount: msgCount,
			})

			// Router answered directly — no second LLM call needed.
			if routeResult.Response != "" && routeResult.Tier == "" {
				log.Printf("→ router direct response")
				eventLog.Log("router_direct", map[string]any{
					"chat_id": chatID,
					"reason":  routeResult.Reason,
				})
				chatSessions.TouchContext(chatID, "router")
				tg.SendMessage(chatID, routeResult.Response)
				continue
			}

			// Resolve tier to params.
			tp = resolveTierParams(routeResult.Tier, tierStore.Current(), tierFS)

			eventLog.Log("router_classify", map[string]any{
				"chat_id": chatID,
				"tier":    routeResult.Tier,
				"reason":  routeResult.Reason,
				"model":   tp.Model,
			})

			start := time.Now()
			result, err := askClaude(u.Message.Text, resumeID, tp)
			// Retry without resume if session not found.
			if err != nil && resumeID != "" && strings.Contains(err.Error(), "No conversation found") {
				log.Printf("session %s expired, starting fresh", resumeID)
				chatSessions.Archive(chatID)
				result, err = askClaude(u.Message.Text, "", tp)
			}
			duration := time.Since(start)

			if err != nil {
				log.Printf("claude error: %v", err)
				reply := fmt.Sprintf("Error: %v", err)
				eventLog.Log("bot_error", map[string]any{
					"context": "askClaude",
					"error":   err.Error(),
					"chat_id": chatID,
				})
				tg.SendHTML(chatID, reply)
				continue
			}

			// Store the session ID returned by Claude for future --resume.
			if result.SessionID != "" {
				isNew := resumeID == ""
				chatSessions.SetWithContext(chatID, result.SessionID, routeResult.Tier)
				if isNew {
					reason := "first"
					if resumeID == "" && len(chatSessions.Get(chatID)) > 0 {
						reason = "timeout"
					}
					eventLog.Log("session_new", map[string]any{
						"chat_id":    chatID,
						"session_id": result.SessionID,
						"reason":     reason,
					})
				}
			}
			chatSessions.Touch(chatID)

			reply := result.Text

			// Detect Claude not logged in.
			lower := strings.ToLower(reply)
			if strings.Contains(lower, "not logged in") || strings.Contains(lower, "authenticate") || strings.Contains(lower, "login required") {
				reply = "Not logged in \u00b7 Please run /login on the host with: alf login"
			}

			log.Printf("→ %s %dms $%.4f", result.Model, duration.Milliseconds(), result.CostUSD)

			eventLog.Log("message_out", map[string]any{
				"chat_id":     chatID,
				"model":       result.Model,
				"duration_ms": duration.Milliseconds(),
				"cost_usd":    result.CostUSD,
				"text_length": len(reply),
				"session_id":  result.SessionID,
			})

			tg.SendMessage(chatID, reply)
		}
	}
}

type jsonModel struct {
	CostUSD float64 `json:"costUSD"`
}

// claudeResult holds parsed output from Claude CLI JSON response.
type claudeResult struct {
	SessionID string
	Text      string
	Model     string
	CostUSD   float64
}

// tierParams holds per-tier Claude CLI arguments.
type tierParams struct {
	Model          string   // full model name, e.g. "claude-sonnet-4-5"
	MaxTurns       int      // 0 = default (3)
	Tools          []string // nil = omit flag
	Effort         string   // "" = omit flag
	TierPromptArgs []string // --append-system-prompt pairs from tier system-prompt.md + skills
}

func askClaude(prompt, resumeID string, tp tierParams) (*claudeResult, error) {
	model := tp.Model
	if model == "" {
		model = "claude-haiku-4-5"
	}
	maxTurns := tp.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 3
	}

	args := []string{
		"-p", prompt,
		"--model", model,
		"--output-format", "json",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
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

	dataDir := "/home/node/data"
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}

	// Inject all memory files as appended system prompts.
	args = append(args, memory.CollectPrompts(filepath.Join(dataDir, "memories"))...)

	// Inject tier-specific system prompt and skills.
	args = append(args, tp.TierPromptArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dataDir
	// Filter out existing HOME to avoid duplicates (first value wins on Linux).
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "HOME=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "HOME="+dataDir)

	log.Printf("askClaude: starting (resume=%q)", resumeID)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("claude timed out after 5 minutes")
	}

	out := strings.TrimSpace(stdout.String())

	// Try to parse JSON output for session_id and result.
	if out != "" {
		var parsed struct {
			SessionID    string               `json:"session_id"`
			Subtype      string               `json:"subtype"`
			Result       string               `json:"result"`
			IsError      bool                 `json:"is_error"`
			TotalCostUSD float64              `json:"total_cost_usd"`
			ModelUsage   map[string]jsonModel `json:"modelUsage"`
		}
		if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr == nil {
			text := parsed.Result
			if text == "" {
				// Handle empty result with a human-readable message.
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
				log.Printf("askClaude: empty result (subtype=%q, is_error=%v)", parsed.Subtype, parsed.IsError)
			}
			// If Claude returned an error result, propagate as error for retry logic.
			if parsed.IsError && strings.Contains(text, "No conversation found") {
				return nil, fmt.Errorf("claude: %s", text)
			}
			// Extract model name from usage.
			model := "unknown"
			for m := range parsed.ModelUsage {
				model = m
				break
			}
			return &claudeResult{
				SessionID: parsed.SessionID,
				Text:      text,
				Model:     model,
				CostUSD:   parsed.TotalCostUSD,
			}, nil
		}
		// JSON parse failed — treat raw output as text response.
		return &claudeResult{Text: out}, nil
	}

	// No stdout — check stderr.
	errOut := strings.TrimSpace(stderr.String())
	if err != nil {
		if errOut != "" {
			return nil, fmt.Errorf("claude: %s", errOut)
		}
		return nil, fmt.Errorf("claude failed: %v", err)
	}

	return nil, fmt.Errorf("claude returned empty response")
}

func readSecret(envVar string) string {
	if path := os.Getenv(envVar + "_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(os.Getenv(envVar))
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	Chat Chat   `json:"chat"`
	From User   `json:"from"`
	Text string `json:"text"`
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

func getUpdates(client *http.Client, token string, offset int64) ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

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

// handleCommand processes known /commands. Returns true if handled.
func handleCommand(tg *tgclient.Client, msg *Message, chatSessions *session.Store, eventLog *eventlog.Logger, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) bool {
	cmd := strings.SplitN(msg.Text, " ", 2)[0]
	switch cmd {
	case "/login":
		handleLogin(tg, msg, magic, ccExternalURL, allowedChatIDs)
		return true
	case "/new":
		old := chatSessions.Archive(msg.Chat.ID)
		reply := "New session started."
		if old != "" {
			reply = "Previous session archived. New session started."
			eventLog.Log("session_archived", map[string]any{
				"chat_id":        msg.Chat.ID,
				"old_session_id": old,
			})
		}
		tg.SendHTML(msg.Chat.ID, reply)
		return true
	case "/start":
		tg.SendHTML(msg.Chat.ID, "Hello! I'm ALF, your AI assistant. Send me a message and I'll respond using Claude.")
		return true
	case "/restart":
		tg.SendHTML(msg.Chat.ID, "Restarting ALF daemon...")
		log.Println("restart requested via /restart command")
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(0)
		}()
		return true
	case "/help":
		help := "<b>Available commands:</b>\n" +
			"/help — Show this message\n" +
			"/new — Start a new conversation session\n" +
			"/restart — Restart the ALF daemon\n" +
			"/login — Get a login link for the Control Center\n" +
			"/start — Welcome message"
		tg.SendHTML(msg.Chat.ID, help)
		return true
	}
	return false
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
	tg.SendHTML(chatID, fmt.Sprintf("Session: %s · Expires in 5 min\n%s", label, link))
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

func isInstantTier(tierName string, tiers *cc.TiersConfig) bool {
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			return t.Instant
		}
	}
	return false
}

func resolveTierParams(tierName string, tiers *cc.TiersConfig, tfs *tierfs.TierFS) tierParams {
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			return tierParams{
				Model:          router.ResolveModel(t.Model),
				MaxTurns:       t.MaxTurns,
				Tools:          t.Tools,
				Effort:         t.Effort,
				TierPromptArgs: tfs.CollectPromptArgs(tierName),
			}
		}
	}
	// Tier not found — use defaults.
	return tierParams{Model: "claude-haiku-4-5"}
}

// migrateConfig copies config files from old data/config/ to configDir on first run.
func migrateConfig(dataDir, configDir string) {
	oldConfigDir := filepath.Join(dataDir, "config")

	// Config files: copy if missing in configDir.
	for _, name := range []string{"config.json", "tiers.json", "router-prompt.md"} {
		dst := filepath.Join(configDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // already exists
		}
		src := filepath.Join(oldConfigDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // no old file
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			log.Printf("migrate: failed to copy %s: %v", name, err)
			continue
		}
		log.Printf("migrate: %s → %s", src, dst)
	}

	// Tier directories: copy from data/tiers/ to configDir/tiers/ if needed.
	oldTiersDir := filepath.Join(dataDir, "tiers")
	newTiersDir := filepath.Join(configDir, "tiers")
	entries, err := os.ReadDir(oldTiersDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dst := filepath.Join(newTiersDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // already exists
		}
		src := filepath.Join(oldTiersDir, e.Name())
		filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, path)
			target := filepath.Join(dst, rel)
			if info.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode())
		})
		log.Printf("migrate: tier %s → %s", e.Name(), newTiersDir)
	}

	// Clean up orphan directories from old layout.
	for _, orphan := range []string{"tiers", "memory", "state"} {
		p := filepath.Join(dataDir, orphan)
		if _, err := os.Stat(p); err == nil {
			if err := os.RemoveAll(p); err != nil {
				log.Printf("migrate: failed to remove old %s: %v", orphan, err)
			} else {
				log.Printf("migrate: removed orphan %s/", orphan)
			}
		}
	}
}

func parseAllowedChatIDs(s string) map[int64]bool {
	result := make(map[int64]bool)
	if s == "" {
		return result
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			result[id] = true
		}
	}
	return result
}

