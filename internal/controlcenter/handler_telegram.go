package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/vault"
)

// TelegramHandler handles GET, PUT, DELETE /api/telegram for configuring Telegram integration.
// Bot token is stored in the vault (encrypted). Chat ID is stored in the vault too.
// Fallback: reads Docker secrets for backward compatibility with alf init setups.
type TelegramHandler struct {
	Vault *vault.Manager
}

const (
	vaultKeyTGBotToken = "telegram_bot_token"
	vaultKeyTGChatID   = "telegram_chat_id"
)

func (h *TelegramHandler) loadToken() string {
	// Primary: vault.
	if h.Vault != nil {
		if v, err := h.Vault.GetSecret(vaultKeyTGBotToken); err == nil && v != "" {
			return v
		}
	}
	// Fallback: Docker secrets.
	return readSecretEnv("TELEGRAM_BOT_TOKEN")
}

func (h *TelegramHandler) loadChatID() string {
	if h.Vault != nil {
		if v, err := h.Vault.GetSecret(vaultKeyTGChatID); err == nil && v != "" {
			return v
		}
	}
	return readSecretEnv("TELEGRAM_CHAT_ID")
}

// readSecretEnv reads a secret from a *_FILE env var (Docker secrets) or the env var directly.
func readSecretEnv(envVar string) string {
	if path := os.Getenv(envVar + "_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(os.Getenv(envVar))
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
	token := h.loadToken()
	chatID := h.loadChatID()

	resp := map[string]any{
		"configured": token != "" && chatID != "",
		"chat_id":    chatID,
	}
	if token != "" {
		if len(token) > 12 {
			resp["bot_token_masked"] = token[:8] + "..." + token[len(token)-4:]
		} else {
			resp["bot_token_masked"] = "***"
		}
		if name := validateBotTokenHTTP(token); name != "" {
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

	// Validate bot token against Telegram API.
	botName := validateBotTokenHTTP(req.BotToken)
	if botName == "" {
		http.Error(w, jsonErr("invalid bot token - could not verify with Telegram API"), http.StatusBadRequest)
		return
	}

	if h.Vault == nil {
		http.Error(w, jsonErr("vault not available"), http.StatusServiceUnavailable)
		return
	}

	// Store in vault.
	if err := h.Vault.SetSecret(vaultKeyTGBotToken, req.BotToken); err != nil {
		http.Error(w, jsonErr(fmt.Sprintf("failed to save bot token: %v", err)), http.StatusInternalServerError)
		return
	}
	if err := h.Vault.SetSecret(vaultKeyTGChatID, req.ChatID); err != nil {
		http.Error(w, jsonErr(fmt.Sprintf("failed to save chat ID: %v", err)), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":               true,
		"bot_name":         botName,
		"restart_required": true,
	})
}

func (h *TelegramHandler) del(w http.ResponseWriter, _ *http.Request) {
	if h.Vault != nil {
		h.Vault.Client().DeleteFile(vaultKeyTGBotToken)
		h.Vault.Client().DeleteFile(vaultKeyTGChatID)
	}
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
