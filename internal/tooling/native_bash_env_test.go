package tooling

import (
	"strings"
	"testing"
)

func hasVar(env []string, prefix string) bool {
	for _, v := range env {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// lastVar returns the value of the LAST env entry starting with prefix.
// This matches shell semantics where later env entries override earlier ones.
func lastVar(env []string, prefix string) string {
	val := ""
	for _, v := range env {
		if strings.HasPrefix(v, prefix) {
			val = strings.TrimPrefix(v, prefix)
		}
	}
	return val
}

func TestBashSafeEnv_OverridesUserIdentity(t *testing.T) {
	// With a data dir, PATH must be prepended with tools.d/tools.
	env := bashSafeEnv("/data")

	// Core identity variables are forced to the alf user.
	if lastVar(env,"USER=") != "alf" {
		t.Errorf("USER must be alf, got %q", lastVar(env,"USER="))
	}
	if lastVar(env,"LOGNAME=") != "alf" {
		t.Errorf("LOGNAME must be alf, got %q", lastVar(env,"LOGNAME="))
	}
	if lastVar(env,"TERM=") != "xterm-256color" {
		t.Errorf("TERM must be set to xterm-256color, got %q", lastVar(env,"TERM="))
	}
	// HOME is always present (either from env or the baked-in default).
	if !hasVar(env, "HOME=") {
		t.Error("HOME must be set")
	}

	// PATH must be prepended with the data tool dirs.
	path := lastVar(env,"PATH=")
	if !strings.Contains(path, "/data/tools.d") || !strings.Contains(path, "/data/tools") {
		t.Errorf("PATH must include tool dirs, got %q", path)
	}
}

func TestBashSafeEnv_NoDataDirSkipsPATH(t *testing.T) {
	// When dataDir is empty, the PATH prepend branch is skipped. If the host
	// process has no PATH set, the returned env may also have no PATH — the
	// function's only commitment is "don't prepend tool dirs".
	env := bashSafeEnv("")
	path := lastVar(env,"PATH=")
	if strings.Contains(path, "tools.d") {
		t.Errorf("PATH should not be prepended with tool dirs when dataDir is empty, got %q", path)
	}
}

func TestBashSafeEnv_DoesNotLeakSecrets(t *testing.T) {
	// The allowlist filter must drop anything that doesn't match a safe prefix.
	// The test process itself may have unrelated env vars (like GOPATH) — we
	// assert none of them leak.
	env := bashSafeEnv("/data")
	forbiddenPrefixes := []string{
		"VAULT_TOKEN=", "CLAUDE_", "ANTHROPIC_", "API_KEY=", "OPENAI_",
		"GOPATH=", "GOROOT=", "XDG_",
	}
	for _, v := range env {
		for _, bad := range forbiddenPrefixes {
			if strings.HasPrefix(v, bad) {
				t.Errorf("forbidden var leaked: %s", v)
			}
		}
	}
}
