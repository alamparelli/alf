package controlcenter

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/vault"
)

// validateBaseURL checks that a user-supplied base URL is safe to send requests to.
// Blocks private/link-local IPs and cloud metadata endpoints to prevent SSRF.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname")
	}

	// Block cloud metadata endpoints.
	metadataHosts := []string{"169.254.169.254", "metadata.google.internal", "metadata.internal"}
	for _, m := range metadataHosts {
		if strings.EqualFold(host, m) {
			return fmt.Errorf("blocked: cloud metadata endpoint")
		}
	}

	// Resolve and check for private/loopback/link-local IPs.
	// Allow "host.docker.internal" (used for Ollama).
	if strings.EqualFold(host, "host.docker.internal") {
		return nil
	}
	ips, err := net.LookupHost(host)
	if err != nil {
		// DNS failure is not an SSRF — let the caller handle connection errors.
		return nil
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("blocked: %s resolves to non-routable address %s", host, ipStr)
		}
	}
	return nil
}

// SetupHandler serves the setup wizard API endpoints.
type SetupHandler struct {
	ConfigStore ConfigStore
	TierStore   TierStore
	Vault       *vault.Manager // nil-safe
	PresetsDir  string         // path to config.d/setup-presets/
	Notifier    Notifier       // nil-safe — for reload events
	ConfigDir   string         // config.d/ path — for tiers file resolution
	OnVaultUnlock func()       // nil-safe — called after vault unlock (secret migration)
	DataDir     string         // data dir — for toolbox regeneration
}

func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip the /api/setup/ prefix to get the sub-route.
	sub := strings.TrimPrefix(r.URL.Path, "/api/setup/")
	sub = strings.TrimSuffix(sub, "/")

	switch {
	case sub == "status" && r.Method == http.MethodGet:
		h.handleStatus(w, r)
	case sub == "presets" && r.Method == http.MethodGet:
		h.handlePresets(w, r)
	case sub == "backend/test" && r.Method == http.MethodPost:
		h.handleBackendTest(w, r)
	case sub == "telegram/validate" && r.Method == http.MethodPost:
		h.handleTelegramValidate(w, r)
	case sub == "telegram/chatid" && r.Method == http.MethodPost:
		h.handleTelegramChatID(w, r)
	case sub == "claude/check" && r.Method == http.MethodGet:
		h.handleClaudeCheck(w, r)
	case sub == "ollama/models" && r.Method == http.MethodGet:
		h.handleOllamaModels(w, r)
	case sub == "apply" && r.Method == http.MethodPost:
		h.handleApply(w, r)
	default:
		methodNotAllowed(w)
	}
}

// SetupStatus represents the current state of the setup wizard.
type SetupStatus struct {
	Steps     SetupSteps `json:"steps"`
	Completed bool       `json:"completed"`
}

// SetupSteps tracks which setup steps are done.
type SetupSteps struct {
	Backend    bool `json:"backend"`
	ClaudeAuth bool `json:"claude_auth"`
	Telegram   bool `json:"telegram"`
	Tiers      bool `json:"tiers"`
}

func (h *SetupHandler) handleStatus(w http.ResponseWriter, _ *http.Request) {
	var steps SetupSteps

	// Claude auth: check if $HOME/.claude.json exists and is non-trivial.
	steps.ClaudeAuth = isClaudeAuthFile()

	// Backend: at least one API backend configured OR Claude CLI authenticated.
	cfg, err := h.ConfigStore.Load()
	if err == nil && len(cfg.Backends) > 0 {
		steps.Backend = true
	}
	if steps.ClaudeAuth {
		steps.Backend = true
	}

	// Telegram: bot token AND chat ID present.
	steps.Telegram = h.isTelegramConfigured()

	// Tiers: at least one enabled tier.
	if tc := h.TierStore.Current(); tc != nil {
		for _, t := range tc.Tiers {
			if t.Enabled {
				steps.Tiers = true
				break
			}
		}
	}

	status := SetupStatus{
		Steps:     steps,
		Completed: steps.Backend && steps.Tiers,
	}

	respondJSON(w, http.StatusOK, status)
}

// isClaudeAuthFile checks if the Claude CLI auth file exists and has content.
func isClaudeAuthFile() bool {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/alf"
	}
	info, err := os.Stat(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}
	return info.Size() > 2 // non-empty (not just "{}")
}

// ensureClaudeOnboarding patches .claude.json to mark onboarding as complete.
// Claude CLI requires hasCompletedOnboarding=true before accepting -p invocations.
// Users who authenticate via "claude login" in the terminal get a valid OAuth token
// but skip the interactive onboarding, leaving this flag unset.
func ensureClaudeOnboarding() {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/alf"
	}
	path := filepath.Join(home, ".claude.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var cfg map[string]any
	if json.Unmarshal(data, &cfg) != nil {
		return
	}

	changed := false

	if v, ok := cfg["hasCompletedOnboarding"]; !ok || v != true {
		cfg["hasCompletedOnboarding"] = true
		changed = true
	}

	if _, ok := cfg["numStartups"]; !ok {
		cfg["numStartups"] = 1
		changed = true
	}

	if !changed {
		return
	}

	patched, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}

	if err := os.WriteFile(path, patched, 0o640); err != nil {
		log.Printf("[setup] failed to patch .claude.json onboarding: %v", err)
	} else {
		log.Printf("[setup] patched .claude.json: hasCompletedOnboarding=true")
	}
}

// isTelegramConfigured checks vault then Docker secrets for both token and chat ID.
func (h *SetupHandler) isTelegramConfigured() bool {
	token := h.loadTelegramSecret(vaultKeyTGBotToken, "TELEGRAM_BOT_TOKEN")
	chatID := h.loadTelegramSecret(vaultKeyTGChatID, "TELEGRAM_CHAT_ID")
	return token != "" && chatID != ""
}

func (h *SetupHandler) loadTelegramSecret(vaultKey, envVar string) string {
	if h.Vault != nil {
		if v, err := h.Vault.GetSecret(vaultKey); err == nil && v != "" {
			return v
		}
	}
	return readSecretEnv(envVar)
}

func (h *SetupHandler) handlePresets(w http.ResponseWriter, _ *http.Request) {
	presets, err := loadPresetsFromDir(h.PresetsDir)
	if err != nil {
		log.Printf("[setup] warning: failed to load presets: %v", err)
	}
	if len(presets) == 0 {
		presets = loadEmbeddedPresets()
	}
	if presets == nil {
		presets = make(map[string][]TierPreset)
	}
	respondJSON(w, http.StatusOK, map[string]any{"presets": presets})
}

// --- PR2: Validation endpoints (stateless, no side effects) ---

// handleBackendTest tests connectivity to an LLM backend.
func (h *SetupHandler) handleBackendTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string `json:"type"`     // "openrouter", "openai", "ollama", "custom"
		BaseURL string `json:"base_url"` // required
		APIKey  string `json:"api_key"`  // optional for ollama
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, jsonErr("invalid JSON"), http.StatusBadRequest)
		return
	}
	req.BaseURL = strings.TrimSpace(strings.TrimSuffix(req.BaseURL, "/"))
	if req.BaseURL == "" {
		http.Error(w, jsonErr("base_url is required"), http.StatusBadRequest)
		return
	}
	if err := validateBaseURL(req.BaseURL); err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Test by calling /models endpoint (OpenAI-compatible).
	modelsURL := req.BaseURL + "/models"
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, modelsURL, nil)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("invalid URL: %v", err)})
		return
	}
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("connection failed: %v", err)})
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "authentication failed - check your API key"})
		return
	}
	if resp.StatusCode >= 400 {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("backend returned HTTP %d", resp.StatusCode)})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTelegramValidate validates a bot token via the Telegram API.
func (h *SetupHandler) handleTelegramValidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, jsonErr("invalid JSON"), http.StatusBadRequest)
		return
	}
	req.BotToken = strings.TrimSpace(req.BotToken)
	if req.BotToken == "" {
		http.Error(w, jsonErr("bot_token is required"), http.StatusBadRequest)
		return
	}

	botName := validateBotTokenHTTP(req.BotToken)
	if botName == "" {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "invalid bot token"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "bot_name": botName})
}

// handleClaudeCheck returns whether Claude CLI is authenticated.
func (h *SetupHandler) handleClaudeCheck(w http.ResponseWriter, _ *http.Request) {
	auth := isClaudeAuthFile()
	if auth {
		ensureClaudeOnboarding()
	}
	respondJSON(w, http.StatusOK, map[string]bool{"authenticated": auth})
}

// handleOllamaModels lists models installed on an Ollama instance.
func (h *SetupHandler) handleOllamaModels(w http.ResponseWriter, r *http.Request) {
	baseURL := strings.TrimSpace(strings.TrimSuffix(r.URL.Query().Get("base_url"), "/"))
	if baseURL == "" {
		baseURL = "http://host.docker.internal:11434"
	}
	if err := validateBaseURL(baseURL); err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": err.Error()})
		return
	}

	tagsURL := baseURL + "/api/tags"
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, tagsURL, nil)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": fmt.Sprintf("invalid URL: %v", err)})
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": fmt.Sprintf("cannot reach Ollama: %v", err)})
		return
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"models": []string{}, "error": "invalid response from Ollama"})
		return
	}

	names := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		names = append(names, m.Name)
	}
	respondJSON(w, http.StatusOK, map[string]any{"models": names})
}

// --- PR3: Apply endpoint ---

// setupApplyRequest is the input for POST /api/setup/apply.
type setupApplyRequest struct {
	Backends      map[string]setupBackend `json:"backends,omitempty"`
	Telegram      *setupTelegram          `json:"telegram,omitempty"`
	PresetID      string                  `json:"preset_id,omitempty"`
	Timezone      string                  `json:"timezone,omitempty"`
	VaultPassword string                  `json:"vault_password,omitempty"`
}

type setupBackend struct {
	BaseURL      string            `json:"base_url"`
	APIKey       string            `json:"api_key,omitempty"`
	Auth         string            `json:"auth,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	DefaultModel string            `json:"default_model,omitempty"`
}

type setupTelegram struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

func (h *SetupHandler) handleApply(w http.ResponseWriter, r *http.Request) {
	var req setupApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, jsonErr("invalid JSON"), http.StatusBadRequest)
		return
	}

	// Validate timezone if provided.
	if req.Timezone != "" {
		if _, err := time.LoadLocation(req.Timezone); err != nil {
			http.Error(w, jsonErr(fmt.Sprintf("invalid timezone %q", req.Timezone)), http.StatusBadRequest)
			return
		}
	}

	// Unlock/initialize vault if password provided (always, not just when secrets are needed).
	needsVault := h.applyNeedsVault(req)
	vaultUnlocked := false

	if req.VaultPassword != "" && h.Vault != nil && h.Vault.AdminToken() == "" {
		if err := h.Vault.AutoUnlock(req.VaultPassword); err != nil {
			http.Error(w, jsonErr("vault unlock failed: "+err.Error()), http.StatusBadRequest)
			return
		}
		if _, err := h.Vault.CreateProxyToken(); err != nil {
			http.Error(w, jsonErr("vault proxy token failed: "+err.Error()), http.StatusInternalServerError)
			return
		}
		// Persist password for auto-unlock on restart.
		if pwFile := h.Vault.PasswordFile(); pwFile != "" {
			if err := os.WriteFile(pwFile, []byte(req.VaultPassword), 0o600); err != nil {
				log.Printf("[setup] warning: failed to persist vault password: %v", err)
			}
		}
		// Note: LLM subprocesses access vault via VAULT_PROXY_SOCK (set by daemon).
		vaultUnlocked = true
		if h.OnVaultUnlock != nil {
			h.OnVaultUnlock()
		}
	}

	// If secrets need storing but vault is still locked, fail early.
	if needsVault && h.Vault != nil && h.Vault.AdminToken() == "" {
		http.Error(w, jsonErr("vault is locked — provide vault_password to unlock"), http.StatusServiceUnavailable)
		return
	}
	if needsVault && h.Vault == nil {
		http.Error(w, jsonErr("vault not available — cannot store API keys"), http.StatusServiceUnavailable)
		return
	}

	// Store API keys in vault.
	for name, b := range req.Backends {
		if b.APIKey == "" {
			continue // don't overwrite existing key
		}
		auth := b.Auth
		if auth == "" {
			auth = "bearer"
		}
		if auth == "none" {
			continue // no key needed
		}
		vaultKey := name + "_api_key"
		if err := h.Vault.SetSecret(vaultKey, b.APIKey); err != nil {
			http.Error(w, jsonErr(fmt.Sprintf("failed to store %s API key: %v", name, err)), http.StatusInternalServerError)
			return
		}
	}

	// Store Telegram credentials in vault.
	restartRequired := false
	if req.Telegram != nil && req.Telegram.BotToken != "" && req.Telegram.ChatID != "" {
		if err := h.Vault.SetSecret(vaultKeyTGBotToken, req.Telegram.BotToken); err != nil {
			http.Error(w, jsonErr("failed to store Telegram bot token: "+err.Error()), http.StatusInternalServerError)
			return
		}
		if err := h.Vault.SetSecret(vaultKeyTGChatID, req.Telegram.ChatID); err != nil {
			http.Error(w, jsonErr("failed to store Telegram chat ID: "+err.Error()), http.StatusInternalServerError)
			return
		}
		restartRequired = true // Telegram bot polling doesn't hot-reload
	}

	// Merge config.json: load existing, merge backends (without api_key) + timezone.
	configChanged := false
	if len(req.Backends) > 0 || req.Timezone != "" {
		cfg, err := h.ConfigStore.Load()
		if err != nil {
			http.Error(w, jsonErr("failed to load config: "+err.Error()), http.StatusInternalServerError)
			return
		}
		if cfg.Backends == nil {
			cfg.Backends = make(map[string]BackendConfig)
		}
		for name, b := range req.Backends {
			auth := b.Auth
			if auth == "" && name != "ollama" {
				auth = "bearer"
			}
			if name == "ollama" && auth == "" {
				auth = "none"
			}
			cfg.Backends[name] = BackendConfig{
				BaseURL:      b.BaseURL,
				Headers:      b.Headers,
				DefaultModel: b.DefaultModel,
				Auth:         auth,
			}
		}
		if req.Timezone != "" {
			cfg.Timezone = req.Timezone
		}
		if err := h.ConfigStore.Save(cfg); err != nil {
			http.Error(w, jsonErr("failed to save config: "+err.Error()), http.StatusInternalServerError)
			return
		}
		configChanged = true
	}

	// Write tiers config from preset into config.d/tiers/<name>.json and switch to it.
	tiersChanged := false
	if req.PresetID != "" {
		preset, err := h.findPreset(req.PresetID)
		if err != nil {
			http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
			return
		}
		tc := &TiersConfig{Tiers: preset.Tiers}
		if preset.RouterConfig != nil {
			tc.RouterModel = preset.RouterConfig.RouterModel
			tc.RouterBackend = preset.RouterConfig.RouterBackend
			tc.DefaultFallback = preset.RouterConfig.DefaultFallback
			tc.RouterDistinctions = preset.RouterConfig.Distinctions
		}

		// Save as named profile in tiers/ subdirectory.
		tiersDir := filepath.Join(h.ConfigDir, "tiers")
		os.MkdirAll(tiersDir, 0o750)
		profileName := preset.ID + ".json"
		profilePath := filepath.Join(tiersDir, profileName)
		data, _ := json.MarshalIndent(tc, "", "  ")
		if err := os.WriteFile(profilePath, data, 0o644); err != nil {
			http.Error(w, jsonErr("failed to save tier profile: "+err.Error()), http.StatusInternalServerError)
			return
		}

		// Update tiers_file in config.json to point to the new profile.
		if tierCfg, err := h.ConfigStore.Load(); err == nil {
			tierCfg.TiersFile = profileName
			if err := h.ConfigStore.Save(tierCfg); err != nil {
				log.Printf("[setup] warning: failed to update tiers_file in config: %v", err)
			}
		}

		// Switch the tier store to the new profile.
		if err := h.TierStore.SetPath(profilePath); err != nil {
			http.Error(w, jsonErr("failed to switch tiers: "+err.Error()), http.StatusInternalServerError)
			return
		}
		tiersChanged = true
		log.Printf("[setup] tier profile %s saved and activated", profileName)

		// If the preset uses codex backend, ensure Node.js runtime and codex CLI are requested.
		if preset.Backend == "codex" {
			h.ensureCodexRuntime()
			restartRequired = true
		}
	}

	// Ensure Claude onboarding is marked complete when Claude backend is selected.
	// Users who authenticate via "claude login" in the terminal get a valid OAuth
	// token but skip the interactive onboarding that sets hasCompletedOnboarding.
	if isClaudeAuthFile() {
		ensureClaudeOnboarding()
	}

	// Notify daemon for hot-reload.
	if h.Notifier != nil {
		if configChanged {
			h.Notifier.Notify(ReloadConfig)
		}
		if tiersChanged {
			h.Notifier.Notify(ReloadTiers)
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restartRequired,
		"vault_unlocked":   vaultUnlocked,
	})
}

// applyNeedsVault returns true if the request contains secrets that need vault storage.
func (h *SetupHandler) applyNeedsVault(req setupApplyRequest) bool {
	for _, b := range req.Backends {
		if b.APIKey != "" {
			auth := b.Auth
			if auth == "" {
				auth = "bearer"
			}
			if auth != "none" {
				return true
			}
		}
	}
	if req.Telegram != nil && req.Telegram.BotToken != "" {
		return true
	}
	return false
}

// findPreset looks up a preset by ID from the presets directory.
func (h *SetupHandler) findPreset(id string) (*TierPreset, error) {
	presets, err := loadPresetsFromDir(h.PresetsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load presets: %w", err)
	}
	for _, group := range presets {
		for i := range group {
			if group[i].ID == id {
				return &group[i], nil
			}
		}
	}
	// Fallback to embedded presets.
	for _, group := range loadEmbeddedPresets() {
		for i := range group {
			if group[i].ID == id {
				return &group[i], nil
			}
		}
	}
	return nil, fmt.Errorf("preset %q not found", id)
}

// handleTelegramChatID calls Telegram getUpdates to retrieve the chat ID of the
// most recent message sent to the bot. The user should send a message to the bot
// before clicking "Get Chat ID".
func (h *SetupHandler) handleTelegramChatID(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, jsonErr("invalid JSON"), http.StatusBadRequest)
		return
	}
	req.BotToken = strings.TrimSpace(req.BotToken)
	if req.BotToken == "" {
		http.Error(w, jsonErr("bot_token is required"), http.StatusBadRequest)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?limit=5&offset=-5", req.BotToken))
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "failed to reach Telegram API"})
		return
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result []struct {
			Message struct {
				Chat struct {
					ID        int64  `json:"id"`
					FirstName string `json:"first_name"`
					Username  string `json:"username"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || !result.OK {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "invalid response from Telegram"})
		return
	}

	if len(result.Result) == 0 {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "No messages found. Send a message to the bot first, then retry."})
		return
	}

	// Return the most recent chat ID.
	last := result.Result[len(result.Result)-1]
	chatID := last.Message.Chat.ID
	name := last.Message.Chat.FirstName
	if name == "" {
		name = last.Message.Chat.Username
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"chat_id": fmt.Sprintf("%d", chatID),
		"name":    name,
	})
}

// ensureCodexRuntime writes runtime.txt and npm-global.txt so the entrypoint
// installs Node.js and the Codex CLI on next container start.
func (h *SetupHandler) ensureCodexRuntime() {
	runtimePath := filepath.Join(h.ConfigDir, "runtime.txt")
	npmPath := filepath.Join(h.ConfigDir, "npm-global.txt")

	if err := os.WriteFile(runtimePath, []byte("node\n"), 0o644); err != nil {
		log.Printf("[setup] warning: failed to write runtime.txt: %v", err)
	}
	if err := os.WriteFile(npmPath, []byte("@openai/codex\n"), 0o644); err != nil {
		log.Printf("[setup] warning: failed to write npm-global.txt: %v", err)
	}
	log.Println("[setup] codex runtime files written (restart required)")
}

// loadPresetsFromDir reads all *.json files from dir and groups them by backend.
func loadPresetsFromDir(dir string) (map[string][]TierPreset, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	result := make(map[string][]TierPreset)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Printf("[setup] warning: skipping preset %s: %v", e.Name(), err)
			continue
		}
		var p TierPreset
		if err := json.Unmarshal(data, &p); err != nil {
			log.Printf("[setup] warning: invalid preset %s: %v", e.Name(), err)
			continue
		}
		if p.ID == "" || p.Backend == "" {
			log.Printf("[setup] warning: preset %s missing id or backend, skipping", e.Name())
			continue
		}
		result[p.Backend] = append(result[p.Backend], p)
	}
	return result, nil
}
