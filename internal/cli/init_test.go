package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: create a bufio.Reader from simulated user input lines.
func inputReader(lines ...string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

func TestPromptBackends_EmptyInput_NoPrevious(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	reader := inputReader("") // just press Enter
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 0 {
		t.Errorf("expected no configured backends, got %v", profile.ConfiguredBackends)
	}
	if profile.OpenRouterKey {
		t.Error("expected OpenRouterKey to remain false")
	}
}

func TestPromptBackends_EmptyInput_WithPreviousOpenRouterKey(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{OpenRouterKey: true}

	reader := inputReader("") // press Enter keeps existing
	promptBackends(reader, dir, &profile)

	if !profile.OpenRouterKey {
		t.Error("expected OpenRouterKey to remain true")
	}
	// ConfiguredBackends is not modified on empty input — only OpenRouterKey
	// signals existing config via backward compat.
}

func TestPromptBackends_EmptyInput_WithPreviousConfiguredBackends(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{ConfiguredBackends: []string{"openrouter", "openai"}}

	reader := inputReader("")
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 2 {
		t.Errorf("expected 2 configured backends preserved, got %v", profile.ConfiguredBackends)
	}
}

func TestPromptBackends_Choice1_OpenRouter(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	// Choice "1" selects OpenRouter, then provide an API key.
	reader := inputReader("1", "sk-or-test-key-12345")
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 1 || profile.ConfiguredBackends[0] != "openrouter" {
		t.Errorf("expected [openrouter], got %v", profile.ConfiguredBackends)
	}

	// Verify secret was written.
	got := GetSecret(dir, "openrouter_api_key")
	if got != "sk-or-test-key-12345" {
		t.Errorf("expected openrouter secret 'sk-or-test-key-12345', got %q", got)
	}

	// Backward compat: OpenRouterKey should be set.
	if !profile.OpenRouterKey {
		t.Error("expected OpenRouterKey=true for backward compatibility")
	}
}

func TestPromptBackends_Choice2_OpenAI(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	reader := inputReader("2", "sk-openai-key-abcdef")
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 1 || profile.ConfiguredBackends[0] != "openai" {
		t.Errorf("expected [openai], got %v", profile.ConfiguredBackends)
	}

	got := GetSecret(dir, "openai_api_key")
	if got != "sk-openai-key-abcdef" {
		t.Errorf("expected openai secret 'sk-openai-key-abcdef', got %q", got)
	}

	// OpenAI should not set OpenRouterKey.
	if profile.OpenRouterKey {
		t.Error("expected OpenRouterKey=false when only openai is configured")
	}
}

func TestPromptBackends_Choice4_Skip(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	// Choice "4" is Skip (len(knownBackends)+1 = 4).
	reader := inputReader("4")
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 0 {
		t.Errorf("expected no configured backends on skip, got %v", profile.ConfiguredBackends)
	}
}

func TestPromptBackends_MultipleChoices(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	// "1,2" selects OpenRouter then OpenAI; each needs an API key line.
	reader := inputReader("1,2", "sk-or-multi-key", "sk-openai-multi-key")
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 2 {
		t.Fatalf("expected 2 configured backends, got %v", profile.ConfiguredBackends)
	}
	if profile.ConfiguredBackends[0] != "openrouter" {
		t.Errorf("expected first backend 'openrouter', got %q", profile.ConfiguredBackends[0])
	}
	if profile.ConfiguredBackends[1] != "openai" {
		t.Errorf("expected second backend 'openai', got %q", profile.ConfiguredBackends[1])
	}

	if GetSecret(dir, "openrouter_api_key") != "sk-or-multi-key" {
		t.Error("openrouter secret mismatch")
	}
	if GetSecret(dir, "openai_api_key") != "sk-openai-multi-key" {
		t.Error("openai secret mismatch")
	}
	if !profile.OpenRouterKey {
		t.Error("expected OpenRouterKey=true when openrouter is in configured backends")
	}
}

func TestPromptBackends_BackwardCompat_OpenRouterSetsFlag(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	reader := inputReader("1", "sk-or-compat-key")
	promptBackends(reader, dir, &profile)

	if !profile.OpenRouterKey {
		t.Error("backward compat: OpenRouterKey must be true when openrouter is configured")
	}
}

func TestPromptBackends_InvalidChoice_Skipped(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	// "99" is out of range, should be skipped with a warning.
	reader := inputReader("99")
	promptBackends(reader, dir, &profile)

	if len(profile.ConfiguredBackends) != 0 {
		t.Errorf("expected no backends for invalid choice, got %v", profile.ConfiguredBackends)
	}
}

func TestPromptKnownBackend_NewKey(t *testing.T) {
	dir := t.TempDir()
	var configured []string

	opt := knownBackends[0] // openrouter
	reader := inputReader("sk-or-known-test")
	promptKnownBackend(reader, dir, opt, &configured)

	if len(configured) != 1 || configured[0] != "openrouter" {
		t.Errorf("expected [openrouter], got %v", configured)
	}
	if GetSecret(dir, "openrouter_api_key") != "sk-or-known-test" {
		t.Error("secret not written correctly")
	}
}

func TestPromptKnownBackend_EmptyWithNoExisting(t *testing.T) {
	dir := t.TempDir()
	var configured []string

	opt := knownBackends[1] // openai
	reader := inputReader("") // empty, no existing key
	promptKnownBackend(reader, dir, opt, &configured)

	if len(configured) != 0 {
		t.Errorf("expected no backends when skipping with no existing key, got %v", configured)
	}
}

func TestPromptKnownBackend_EmptyKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	// Pre-set an existing secret.
	if err := SetSecret(dir, "openai_api_key", "sk-existing-key"); err != nil {
		t.Fatal(err)
	}

	var configured []string
	opt := knownBackends[1] // openai
	reader := inputReader("") // press Enter to keep
	promptKnownBackend(reader, dir, opt, &configured)

	if len(configured) != 1 || configured[0] != "openai" {
		t.Errorf("expected [openai] when keeping existing, got %v", configured)
	}
	// Verify the original secret is still there.
	if GetSecret(dir, "openai_api_key") != "sk-existing-key" {
		t.Error("existing secret should not be overwritten")
	}
}

func TestPromptKnownBackend_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	if err := SetSecret(dir, "openrouter_api_key", "sk-or-old"); err != nil {
		t.Fatal(err)
	}

	var configured []string
	opt := knownBackends[0] // openrouter
	reader := inputReader("sk-or-new-key")
	promptKnownBackend(reader, dir, opt, &configured)

	if GetSecret(dir, "openrouter_api_key") != "sk-or-new-key" {
		t.Error("expected secret to be overwritten with new key")
	}
}

func TestPromptKnownBackend_WrongPrefixStillSaves(t *testing.T) {
	dir := t.TempDir()
	var configured []string

	opt := knownBackends[0] // openrouter expects "sk-or-" prefix
	reader := inputReader("wrong-prefix-key")
	promptKnownBackend(reader, dir, opt, &configured)

	// Should still save despite wrong prefix.
	if len(configured) != 1 {
		t.Fatalf("expected 1 configured backend, got %v", configured)
	}
	if GetSecret(dir, "openrouter_api_key") != "wrong-prefix-key" {
		t.Error("key should be saved even with wrong prefix")
	}
}

func TestPromptBackends_SecretFilePermissions(t *testing.T) {
	dir := t.TempDir()
	profile := setupProfile{}

	reader := inputReader("1", "sk-or-perms-test")
	promptBackends(reader, dir, &profile)

	path := filepath.Join(dir, "secrets", "openrouter_api_key")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("secret file not found: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("expected secret file permissions 0600, got %04o", perm)
	}
}

// --- Init wizard regression tests ---

func TestTelegramSecretsOptional(t *testing.T) {
	// Verify Telegram secrets are no longer required in the registry.
	for _, s := range SecretRegistry {
		if s.Name == "telegram_bot_token" && s.Required {
			t.Error("telegram_bot_token should not be required")
		}
		if s.Name == "telegram_chat_id" && s.Required {
			t.Error("telegram_chat_id should not be required")
		}
	}
}

func TestEnsureOptionalSecrets_CreatesTelegramFiles(t *testing.T) {
	dir := t.TempDir()
	ensureOptionalSecrets(dir)

	// Telegram secrets should be created as empty files (for Docker Compose).
	for _, name := range []string{"telegram_bot_token", "telegram_chat_id"} {
		path := filepath.Join(dir, "secrets", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
			continue
		}
		if info.Size() != 0 {
			t.Errorf("expected %s to be empty, got %d bytes", name, info.Size())
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("expected %s permissions 0600, got %04o", name, info.Mode().Perm())
		}
	}
}

func TestGenerateFiles_NoTelegram(t *testing.T) {
	dir := t.TempDir()
	// Create required subdirectories.
	os.MkdirAll(filepath.Join(dir, "config.d"), 0o755)
	os.MkdirAll(filepath.Join(dir, "data"), 0o755)

	composeData := ComposeData{
		Image:         "test:latest",
		CCPort:        "8080",
		CCExternalURL: "http://localhost:8080",
	}

	// Call with empty bot token and chat ID.
	generateFiles(dir, "", "", composeData)

	// Verify no Telegram secrets were written (only cc_auth_token + optional empty files).
	botTokenPath := filepath.Join(dir, "secrets", "telegram_bot_token")
	data, err := os.ReadFile(botTokenPath)
	if err != nil {
		t.Fatalf("telegram_bot_token file should exist (empty): %v", err)
	}
	// Should be empty (created by ensureOptionalSecrets, not by generateFiles).
	if strings.TrimSpace(string(data)) != "" {
		t.Error("telegram_bot_token should be empty when not configured")
	}

	// cc_auth_token should always be generated.
	ccToken := GetSecret(dir, "cc_auth_token")
	if ccToken == "" {
		t.Error("cc_auth_token should always be generated")
	}
	if len(ccToken) < 32 {
		t.Errorf("cc_auth_token should be at least 32 chars, got %d", len(ccToken))
	}
}

func TestGenerateFiles_WithTelegram(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "config.d"), 0o755)
	os.MkdirAll(filepath.Join(dir, "data"), 0o755)

	composeData := ComposeData{
		Image:         "test:latest",
		CCPort:        "8080",
		CCExternalURL: "http://localhost:8080",
	}

	generateFiles(dir, "123:ABCtoken", "999", composeData)

	// Verify Telegram secrets are written.
	if got := GetSecret(dir, "telegram_bot_token"); got != "123:ABCtoken" {
		t.Errorf("expected telegram_bot_token '123:ABCtoken', got %q", got)
	}
	if got := GetSecret(dir, "telegram_chat_id"); got != "999" {
		t.Errorf("expected telegram_chat_id '999', got %q", got)
	}
}

func TestConfigTemplate_EmptyChatID(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "config.d"), 0o755)

	err := RenderConfig(dir, ConfigData{ChatID: "", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("RenderConfig with empty ChatID: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.d", "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `"allowed_chat_ids": []`) {
		t.Errorf("expected empty allowed_chat_ids array, got:\n%s", content)
	}
}

func TestConfigTemplate_WithChatID(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "config.d"), 0o755)

	err := RenderConfig(dir, ConfigData{ChatID: "12345", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "config.d", "config.json"))
	content := string(data)
	if !strings.Contains(content, `"allowed_chat_ids": [12345]`) {
		t.Errorf("expected allowed_chat_ids [12345], got:\n%s", content)
	}
}

func TestSetupProfile_Roundtrip(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), ".alf-setup.json")
	p := setupProfile{
		Dir:      "/tmp/alf",
		BotToken: "tok",
		ChatID:   "123",
		Port:     "8080",
		Timezone: "Europe/Rome",
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(tmpFile, data, 0o600)

	var loaded setupProfile
	raw, _ := os.ReadFile(tmpFile)
	json.Unmarshal(raw, &loaded)

	if loaded.Dir != "/tmp/alf" || loaded.BotToken != "tok" || loaded.ChatID != "123" {
		t.Errorf("profile roundtrip failed: %+v", loaded)
	}
	if loaded.Timezone != "Europe/Rome" {
		t.Errorf("expected timezone Europe/Rome, got %q", loaded.Timezone)
	}
}

func TestPromptTelegram_Skip(t *testing.T) {
	// Simulate user pressing "n" to skip Telegram.
	reader := inputReader("n")
	token, botName, chatID := promptTelegram(reader, "", "")
	if token != "" || botName != "" || chatID != "" {
		t.Errorf("expected all empty on skip, got token=%q botName=%q chatID=%q", token, botName, chatID)
	}
}

func TestPromptTelegram_SkipDefault(t *testing.T) {
	// Simulate user pressing Enter (default is N for new setups).
	reader := inputReader("")
	token, botName, chatID := promptTelegram(reader, "", "")
	if token != "" || botName != "" || chatID != "" {
		t.Errorf("expected all empty on default skip, got token=%q botName=%q chatID=%q", token, botName, chatID)
	}
}
