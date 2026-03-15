package secrets

import (
	"os"
	"strings"
)

// ReadSecret reads a secret from a Docker secret file (NAME_FILE env var)
// or falls back to the plain environment variable.
func ReadSecret(envVar string) string {
	if path := os.Getenv(envVar + "_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(os.Getenv(envVar))
}
