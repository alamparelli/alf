package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TelegramConfig stores Telegram integration settings in config.d/telegram.json.
type TelegramConfig struct {
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
}

// TelegramHandler handles GET and PUT /api/telegram for configuring Telegram integration.
type TelegramHandler struct {
	ConfigDir string
}

func (h *TelegramHandler) configPath() string {
	return filepath.Join(h.ConfigDir, "telegram.json")
}

func (h *TelegramHandler) load() TelegramConfig {
	var cfg TelegramConfig
	data, err := os.ReadFile(h.configPath())
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, &cfg)
	return cfg
}

func (h *TelegramHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, r)
	case http.MethodPut:
		h.put(w, r)
	case http.MethodDelete:
		h.del(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *TelegramHandler) get(w http.ResponseWriter, _ *http.Request) {
	cfg := h.load()

	// Return status with masked secrets.
	resp := map[string]any{
		"configured": cfg.BotToken != "" && cfg.ChatID != "",
		"chat_id":    cfg.ChatID,
	}
	if cfg.BotToken != "" {
		// Mask token: show first 8 chars + last 4.
		if len(cfg.BotToken) > 12 {
			resp["bot_token_masked"] = cfg.BotToken[:8] + "..." + cfg.BotToken[len(cfg.BotToken)-4:]
		} else {
			resp["bot_token_masked"] = "***"
		}
		// Validate token against Telegram API.
		if name := validateBotTokenHTTP(cfg.BotToken); name != "" {
			resp["bot_name"] = name
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *TelegramHandler) put(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BotToken string `json:"bot_token"`
		ChatID   string `json:"chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, jsonErr("invalid JSON"), http.StatusBadRequest)
		return
	}

	req.BotToken = strings.TrimSpace(req.BotToken)
	req.ChatID = strings.TrimSpace(req.ChatID)

	if req.BotToken == "" || req.ChatID == "" {
		http.Error(w, jsonErr("bot_token and chat_id are required"), http.StatusBadRequest)
		return
	}

	// Validate bot token.
	botName := validateBotTokenHTTP(req.BotToken)
	if botName == "" {
		http.Error(w, jsonErr("invalid bot token — could not verify with Telegram API"), http.StatusBadRequest)
		return
	}

	cfg := TelegramConfig{
		BotToken: req.BotToken,
		ChatID:   req.ChatID,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(h.configPath(), data, 0o600); err != nil {
		http.Error(w, jsonErr(fmt.Sprintf("failed to save: %v", err)), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":              true,
		"bot_name":        botName,
		"restart_required": true,
	})
}

func (h *TelegramHandler) del(w http.ResponseWriter, _ *http.Request) {
	os.Remove(h.configPath())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// validateBotTokenHTTP validates a bot token via the Telegram API. Returns bot username or "".
func validateBotTokenHTTP(token string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || !result.OK {
		return ""
	}
	return result.Result.Username
}
