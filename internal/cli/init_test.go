package cli

import (
	"bufio"
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
