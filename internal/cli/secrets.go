package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Secret defines a required secret with its metadata.
type Secret struct {
	Name        string // file name in secrets/ dir and Docker secret name
	Description string // human-readable description
	Required    bool   // whether ALF cannot run without it
}

// SecretRegistry lists all secrets ALF knows about.
// Add new entries here as ALF grows - `alf secret list` picks them up automatically.
var SecretRegistry = []Secret{
	{Name: "telegram_bot_token", Description: "Telegram bot token from @BotFather", Required: false},
	{Name: "telegram_chat_id", Description: "Your Telegram chat ID", Required: false},
	{Name: "cc_auth_token", Description: "Control Center auth token (auto-generated)", Required: false},
	{Name: "openrouter_api_key", Description: "OpenRouter API key (sk-or-...)", Required: false},
	{Name: "openai_api_key", Description: "OpenAI API key (sk-...)", Required: false},
	{Name: "claude_oauth_token", Description: "Claude Code OAuth token (via alf login)", Required: false},
	{Name: "vault_master_password", Description: "Vault master password (enables secrets vault)", Required: false},
	{Name: "whisper_shared_secret", Description: "Whisper service shared secret (auto-generated)", Required: false},
}

func secretsDir(baseDir string) string {
	return filepath.Join(baseDir, "secrets")
}

func secretPath(baseDir, name string) string {
	return filepath.Join(secretsDir(baseDir), name)
}

func secretExists(baseDir, name string) bool {
	info, err := os.Stat(secretPath(baseDir, name))
	return err == nil && !info.IsDir()
}

// SetSecret writes a secret file with mode 600 (owner-only).
func SetSecret(baseDir, name, value string) error {
	dir := secretsDir(baseDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := secretPath(baseDir, name)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(value)+"\n"), 0o600); err != nil {
		return err
	}
	return nil
}

// HardenSecrets fixes permissions on existing secret files (0o600) and the secrets dir (0o700).
// Call during upgrade to fix installs that used 0o644.
func HardenSecrets(baseDir string) {
	dir := secretsDir(baseDir)
	os.Chmod(dir, 0o700)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			os.Chmod(filepath.Join(dir, e.Name()), 0o600)
		}
	}
}

// GetSecret reads a secret value. Returns empty string if not set.
func GetSecret(baseDir, name string) string {
	data, err := os.ReadFile(secretPath(baseDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// RemoveSecret deletes a secret file.
func RemoveSecret(baseDir, name string) error {
	path := secretPath(baseDir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("secret '%s' is not set", name)
	}
	return os.Remove(path)
}

// findSecret looks up a secret by name in the registry.
func findSecret(name string) (*Secret, bool) {
	for _, s := range SecretRegistry {
		if s.Name == name {
			return &s, true
		}
	}
	return nil, false
}

// RunSecretList prints all registered secrets with their status.
func RunSecretList() {
	dir := alfDir()

	fmt.Println()
	fmt.Printf("  %-25s %-10s %s\n", "SECRET", "STATUS", "DESCRIPTION")
	fmt.Printf("  %-25s %-10s %s\n", "------", "------", "-----------")

	missing := 0
	for _, s := range SecretRegistry {
		status := colorGreen + "set" + colorReset
		if !secretExists(dir, s.Name) {
			if s.Required {
				status = colorRed + "missing" + colorReset
				missing++
			} else {
				status = colorDim + "unset" + colorReset
			}
		}
		req := ""
		if s.Required {
			req = " *"
		}
		fmt.Printf("  %-25s %-22s %s%s\n", s.Name, status, s.Description, req)
	}

	if missing > 0 {
		fmt.Printf("\n  %s%d required secret(s) missing.%s Use: alf secret set <name> <value>\n", colorYellow, missing, colorReset)
	}
	fmt.Println()
}

// RunSecretSet sets a secret by name.
func RunSecretSet(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: alf secret set <name> <value>")
		os.Exit(1)
	}

	name := args[0]
	value := args[1]
	dir := alfDir()

	if _, known := findSecret(name); !known {
		PrintWarning(fmt.Sprintf("'%s' is not a registered secret. Setting it anyway.", name))
	}

	if err := SetSecret(dir, name, value); err != nil {
		Fatal(fmt.Sprintf("Failed to set secret: %v", err))
	}
	PrintCheck(fmt.Sprintf("Secret '%s' saved", name))
}

// ensureOptionalSecrets creates empty files for optional secrets that
// don't exist yet. Docker Compose requires secret files to exist even
// if they're empty. This allows "alf secret set <name> <value>" +
// "alf restart" to work without re-running "alf init".
func ensureOptionalSecrets(baseDir string) {
	dir := secretsDir(baseDir)
	os.MkdirAll(dir, 0o700)
	for _, s := range SecretRegistry {
		if s.Required {
			continue
		}
		p := filepath.Join(dir, s.Name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			os.WriteFile(p, []byte(""), 0o600)
		}
	}
}

// RunSecretRemove removes a secret by name.
func RunSecretRemove(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: alf secret remove <name>")
		os.Exit(1)
	}

	name := args[0]
	dir := alfDir()

	if err := RemoveSecret(dir, name); err != nil {
		Fatal(fmt.Sprintf("Failed to remove secret: %v", err))
	}
	PrintCheck(fmt.Sprintf("Secret '%s' removed", name))
}
