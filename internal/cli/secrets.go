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
// Add new entries here as ALF grows — `alf secret list` picks them up automatically.
var SecretRegistry = []Secret{
	{Name: "telegram_bot_token", Description: "Telegram bot token from @BotFather", Required: true},
	{Name: "telegram_chat_id", Description: "Your Telegram chat ID", Required: true},
	{Name: "cc_auth_token", Description: "Control Center auth token (auto-generated)", Required: false},
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

// SetSecret writes a secret file with mode 600.
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
