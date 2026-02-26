package main

import (
	"bytes"
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
	"github.com/alamparelli/alf/internal/session"
)

var version = "dev"

func main() {
	token := readSecret("TELEGRAM_BOT_TOKEN")
	chatID := readSecret("TELEGRAM_CHAT_ID")
	authToken := readSecret("CC_AUTH_TOKEN")

	if token == "" || chatID == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}

	// Verify claude CLI is available.
	if _, err := exec.LookPath("claude"); err != nil {
		log.Fatal("claude CLI not found in PATH")
	}

	// Data directory for config, tiers, logs.
	dataDir := "/home/node/data"
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
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

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		server, err := cc.New(dataDir, stats, version, authToken, reloadCh, magic, sessions)
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

	log.Printf("alf-daemon %s starting...", version)

	// Load initial config.
	configStore := cc.NewFileConfigStore(cc.ConfigPath(dataDir))
	cfg, err := configStore.Load()
	if err != nil {
		log.Printf("warning: failed to load config: %v", err)
		cfg = cc.DefaultConfig()
	}
	model := cfg.Model
	if model == "" {
		model = "sonnet"
	}

	// Ensure data subdirectories exist.
	os.MkdirAll(filepath.Join(dataDir, "logs", "events"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755)

	// Session store for Claude --resume support.
	sessionTimeout := time.Duration(cfg.SessionTimeout) * time.Minute
	if sessionTimeout <= 0 {
		sessionTimeout = 30 * time.Minute
	}
	chatSessions := session.New(dataDir, sessionTimeout)

	// JSONL event logger.
	eventLog := eventlog.New(dataDir)
	defer eventLog.Close()

	var offset int64
	client := &http.Client{Timeout: 35 * time.Second}

	for {
		// Check for reload events (non-blocking).
		select {
		case event := <-reloadCh:
			switch event {
			case cc.ReloadConfig:
				if newCfg, err := configStore.Load(); err == nil {
					cfg = newCfg
					model = cfg.Model
					if model == "" {
						model = "sonnet"
					}
					if cfg.SessionTimeout > 0 {
						chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					}
					log.Printf("config reloaded: model=%s log_level=%s session_timeout=%dm", model, cfg.LogLevel, cfg.SessionTimeout)
				}
			case cc.ReloadTiers:
				log.Println("tiers reloaded")
			case cc.ReloadTools:
				log.Println("tools reloaded")
			case cc.ReloadSkills:
				log.Println("skills reloaded")
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
				handleCallbackQuery(client, token, u.CallbackQuery, magic, ccExternalURL, allowedChatIDs)
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
				if handleCommand(client, token, u.Message, chatSessions, eventLog, magic, ccExternalURL, allowedChatIDs) {
					continue
				}
				// Unknown /commands fall through to Claude.
			}

			sessionID, isNew := chatSessions.GetOrCreate(u.Message.Chat.ID)
			if isNew {
				reason := "timeout"
				if sessionID != "" {
					reason = "first"
				}
				eventLog.Log("session_new", map[string]any{
					"chat_id":    u.Message.Chat.ID,
					"session_id": sessionID,
					"reason":     reason,
				})
			}

			start := time.Now()
			reply, err := askClaude(u.Message.Text, model, sessionID, isNew)
			duration := time.Since(start)
			if err != nil {
				log.Printf("claude error: %v", err)
				reply = fmt.Sprintf("Error: %v", err)
				eventLog.Log("bot_error", map[string]any{
					"context": "askClaude",
					"error":   err.Error(),
					"chat_id": u.Message.Chat.ID,
				})
			}

			chatSessions.Touch(u.Message.Chat.ID)

			// Detect Claude not logged in.
			lower := strings.ToLower(reply)
			if strings.Contains(lower, "not logged in") || strings.Contains(lower, "authenticate") || strings.Contains(lower, "login required") {
				reply = "Not logged in \u00b7 Please run /login on the host with: alf login"
			}

			eventLog.Log("message_out", map[string]any{
				"chat_id":     u.Message.Chat.ID,
				"model":       model,
				"duration_ms": duration.Milliseconds(),
				"text_length": len(reply),
				"session_id":  sessionID,
			})

			sendMessage(client, token, u.Message.Chat.ID, reply)
		}
	}
}

func askClaude(prompt, model, sessionID string, isNew bool) (string, error) {
	args := []string{
		"-p", prompt,
		"--model", model,
		"--output-format", "text",
		"--dangerously-skip-permissions",
	}

	if isNew {
		args = append(args, "--session-id", sessionID)
	} else {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.Command("claude", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Claude CLI may write output to stdout or stderr.
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		out = strings.TrimSpace(stderr.String())
	}

	// If we got output, treat it as a valid response regardless of exit code.
	if out != "" {
		return out, nil
	}

	errOut := strings.TrimSpace(stderr.String())
	if err != nil {
		if errOut != "" {
			return "", fmt.Errorf("claude: %s", errOut)
		}
		return "", fmt.Errorf("claude failed: %v", err)
	}

	return "", fmt.Errorf("claude returned empty response")
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
func handleCommand(client *http.Client, token string, msg *Message, chatSessions *session.Store, eventLog *eventlog.Logger, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) bool {
	cmd := strings.SplitN(msg.Text, " ", 2)[0]
	switch cmd {
	case "/login":
		handleLogin(client, token, msg, magic, ccExternalURL, allowedChatIDs)
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
		sendMessage(client, token, msg.Chat.ID, reply)
		return true
	case "/start":
		sendMessage(client, token, msg.Chat.ID, "Hello! I'm ALF, your AI assistant. Send me a message and I'll respond using Claude.")
		return true
	case "/help":
		help := "Available commands:\n" +
			"/help — Show this message\n" +
			"/new — Start a new conversation session\n" +
			"/login — Get a login link for the Control Center\n" +
			"/start — Welcome message"
		sendMessage(client, token, msg.Chat.ID, help)
		return true
	}
	return false
}

func handleLogin(client *http.Client, token string, msg *Message, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) {
	chatID := msg.Chat.ID

	if len(allowedChatIDs) == 0 {
		sendMessage(client, token, chatID, "Login is not configured. Set ALLOWED_CHAT_IDS to enable it.")
		return
	}

	if !allowedChatIDs[chatID] {
		sendMessage(client, token, chatID, "You are not authorized to access the Control Center.")
		return
	}

	// Send inline keyboard with session duration options.
	sendLoginKeyboard(client, token, chatID)
}

func sendLoginKeyboard(client *http.Client, token string, chatID int64) {
	keyboard := map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "24 hours", "callback_data": "login:24h"},
				{"text": "7 days", "callback_data": "login:7d"},
				{"text": "30 days", "callback_data": "login:30d"},
			},
		},
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload, _ := json.Marshal(map[string]any{
		"chat_id":      chatID,
		"text":         "Choose session duration:",
		"reply_markup": keyboard,
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("sendLoginKeyboard error: %v", err)
		return
	}
	defer resp.Body.Close()
}

func handleCallbackQuery(client *http.Client, token string, cb *CallbackQuery, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) {
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
		sendMessage(client, token, chatID, "You are not authorized to access the Control Center.")
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
		sendMessage(client, token, chatID, "Unknown duration. Send /login to try again.")
		return
	}

	code, err := magic.Issue(chatID, ttl)
	if err != nil {
		log.Printf("magic issue error: %v", err)
		sendMessage(client, token, chatID, "Failed to generate login link. Try again.")
		return
	}

	link := fmt.Sprintf("%s/auth?code=%s", strings.TrimRight(ccExternalURL, "/"), code)
	sendMessage(client, token, chatID, fmt.Sprintf("Session: %s · Expires in 5 min\n%s", label, link))
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

func sendMessage(client *http.Client, token string, chatID int64, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("sendMessage error: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("→ reply sent (chat %d)", chatID)
}
