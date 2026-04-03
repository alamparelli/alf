package controlcenter

import (
	"os"
	"strings"
	"testing"
)

// TestTermSafeEnv_BlocksOAuthToken verifies that CLAUDE_CODE_OAUTH_TOKEN
// is NOT leaked into terminal sessions. Regression test for SEC-007.
func TestTermSafeEnv_BlocksOAuthToken(t *testing.T) {
	os.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "secret-oauth-token")
	defer os.Unsetenv("CLAUDE_CODE_OAUTH_TOKEN")

	env := termSafeEnv("/home/alf", "alf")
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") {
			t.Error("CLAUDE_CODE_OAUTH_TOKEN must not be exposed in terminal env")
		}
	}
}

// TestTermSafeEnv_BlocksAnthropicAPIKey verifies that ANTHROPIC_API_KEY
// is NOT leaked into terminal sessions.
func TestTermSafeEnv_BlocksAnthropicAPIKey(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	env := termSafeEnv("/home/alf", "alf")
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY must not be exposed in terminal env")
		}
	}
}

// TestTermSafeEnv_AllowsSafeClaudeVars verifies that non-secret CLAUDE_/ANTHROPIC_
// vars are still passed through.
func TestTermSafeEnv_AllowsSafeClaudeVars(t *testing.T) {
	os.Setenv("CLAUDE_MODEL", "opus")
	os.Setenv("ANTHROPIC_MODEL", "sonnet")
	os.Setenv("CLAUDE_CONFIG_DIR", "/home/alf/.claude")
	defer func() {
		os.Unsetenv("CLAUDE_MODEL")
		os.Unsetenv("ANTHROPIC_MODEL")
		os.Unsetenv("CLAUDE_CONFIG_DIR")
	}()

	env := termSafeEnv("/home/alf", "alf")
	found := map[string]bool{}
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_MODEL=") {
			found["CLAUDE_MODEL"] = true
		}
		if strings.HasPrefix(e, "ANTHROPIC_MODEL=") {
			found["ANTHROPIC_MODEL"] = true
		}
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			found["CLAUDE_CONFIG_DIR"] = true
		}
	}
	for _, key := range []string{"CLAUDE_MODEL", "ANTHROPIC_MODEL", "CLAUDE_CONFIG_DIR"} {
		if !found[key] {
			t.Errorf("%s should be allowed in terminal env", key)
		}
	}
}

// TestTermSafeEnv_BlocksAllTokenAndKeyVars is a broader regression test
// that ensures no env var containing TOKEN or KEY in the name leaks through.
func TestTermSafeEnv_BlocksAllTokenAndKeyVars(t *testing.T) {
	secrets := []string{
		"CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"CLAUDE_AUTH_TOKEN",
		"ANTHROPIC_SECRET_KEY",
	}
	for _, s := range secrets {
		os.Setenv(s, "secret-value")
	}
	defer func() {
		for _, s := range secrets {
			os.Unsetenv(s)
		}
	}()

	env := termSafeEnv("/home/alf", "alf")
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		name := parts[0]
		for _, s := range secrets {
			if name == s {
				t.Errorf("%s must not be exposed in terminal env", s)
			}
		}
	}
}

// TestTermSafeEnv_IncludesBasicVars verifies standard env vars are present.
func TestTermSafeEnv_IncludesBasicVars(t *testing.T) {
	env := termSafeEnv("/home/alf", "alf")
	required := map[string]bool{
		"HOME":    false,
		"USER":    false,
		"LOGNAME": false,
		"TERM":    false,
	}
	for _, e := range env {
		for key := range required {
			if strings.HasPrefix(e, key+"=") {
				required[key] = true
			}
		}
	}
	for key, found := range required {
		if !found {
			t.Errorf("missing required env var %s", key)
		}
	}
}
