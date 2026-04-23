package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewCLIProvider_CreatesEmptyMCPConfig verifies empty-mcp.json is
// created and EmptyMCPConfig is set so --strict-mcp-config works. See #212.
func TestNewCLIProvider_CreatesEmptyMCPConfig(t *testing.T) {
	home := t.TempDir()
	p := NewCLIProvider(home, home, 0, nil)

	expected := filepath.Join(home, ".claude", "empty-mcp.json")
	if p.EmptyMCPConfig != expected {
		t.Fatalf("EmptyMCPConfig=%q, want %q", p.EmptyMCPConfig, expected)
	}
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("empty-mcp.json not created: %v", err)
	}
	if string(data) != `{"mcpServers":{}}` {
		t.Errorf("empty-mcp.json contents unexpected: %s", data)
	}
}

// TestNewCLIProvider_FreshInstallCreatesClaudeDir verifies that on a
// fresh install where ~/.claude/ does not yet exist, NewCLIProvider
// creates the directory rather than failing silently. See #212.
func TestNewCLIProvider_FreshInstallCreatesClaudeDir(t *testing.T) {
	home := t.TempDir()
	// Do NOT pre-create ~/.claude/ — simulate fresh install.

	p := NewCLIProvider(home, home, 0, nil)

	claudeDir := filepath.Join(home, ".claude")
	if fi, err := os.Stat(claudeDir); err != nil || !fi.IsDir() {
		t.Fatalf(".claude dir not created on fresh install: %v", err)
	}
	if p.EmptyMCPConfig == "" {
		t.Error("EmptyMCPConfig is empty — --strict-mcp-config would break")
	}
	if _, err := os.Stat(p.EmptyMCPConfig); err != nil {
		t.Errorf("empty-mcp.json missing at %q: %v", p.EmptyMCPConfig, err)
	}
}

// TestNewCLIProvider_UnwritableHomeFallsBack verifies that when the home
// directory cannot be written, EmptyMCPConfig is left empty so the CLI
// invocation does not pass --mcp-config with a broken path.
func TestNewCLIProvider_UnwritableHomeFallsBack(t *testing.T) {
	// Point at a path that cannot exist as a directory (parent is a file).
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	os.WriteFile(blocker, []byte("x"), 0o644)
	home := filepath.Join(blocker, "home") // cannot mkdir under a regular file

	p := NewCLIProvider(home, home, 0, nil)

	if p.EmptyMCPConfig != "" {
		t.Errorf("expected EmptyMCPConfig to be empty on mkdir failure, got %q", p.EmptyMCPConfig)
	}
}

func TestNewCLIProvider_DefaultTimeout(t *testing.T) {
	p := NewCLIProvider("/tmp", "/tmp", 0, nil)
	if p.Timeout != 10*time.Minute {
		t.Errorf("expected default timeout 10m, got %v", p.Timeout)
	}
}

func TestNewCLIProvider_CustomTimeout(t *testing.T) {
	p := NewCLIProvider("/tmp", "/tmp", 2*time.Minute, nil)
	if p.Timeout != 2*time.Minute {
		t.Errorf("expected timeout 2m, got %v", p.Timeout)
	}
}

func TestCLIProvider_InvokeNoClaude(t *testing.T) {
	// When 'claude' binary is not available, Invoke should return an error.
	dir := t.TempDir()
	p := NewCLIProvider(dir, dir, 5*time.Second, nil)

	// Override PATH to ensure claude is not found.
	t.Setenv("PATH", t.TempDir())

	_, err := p.Invoke(context.Background(), "hello", Params{}, nil)
	if err == nil {
		t.Fatal("expected error when claude binary not found")
	}
}

func TestCLIProvider_InvokeCancelled(t *testing.T) {
	// Cancelled context should return an error.
	dir := t.TempDir()
	p := NewCLIProvider(dir, dir, 5*time.Second, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.Invoke(ctx, "hello", Params{}, nil)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// TestSafeEnv_IncludesSignalSock verifies that ALF_SIGNAL_SOCK is passed
// through safeEnv. Regression test for #122.
func TestSafeEnv_IncludesSignalSock(t *testing.T) {
	t.Setenv("ALF_SIGNAL_SOCK", "/home/alf/data/signal.sock")

	env := safeEnv("/home/alf", "/home/alf/data")

	found := false
	for _, e := range env {
		if e == "ALF_SIGNAL_SOCK=/home/alf/data/signal.sock" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ALF_SIGNAL_SOCK not found in safeEnv output; notify tool will fail (#122)")
	}
}

// TestSafeEnv_IncludesToolsSock verifies ALF_TOOLS_SOCK is still passed.
func TestSafeEnv_IncludesToolsSock(t *testing.T) {
	t.Setenv("ALF_TOOLS_SOCK", "/home/alf/data/context/tools.sock")

	env := safeEnv("/home/alf", "/home/alf/data")

	found := false
	for _, e := range env {
		if e == "ALF_TOOLS_SOCK=/home/alf/data/context/tools.sock" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ALF_TOOLS_SOCK not found in safeEnv output")
	}
}

func TestCLIProvider_ParamsBuildsArgs(t *testing.T) {
	// Verify that Params fields are used. We can't easily test args without
	// actually running claude, but we can verify the provider handles empty params.
	dir := t.TempDir()
	p := NewCLIProvider(dir, dir, 1*time.Second, nil)

	params := Params{
		Model:         "claude-haiku-4-5",
		Tools:         []string{"Read", "Write"},
		Effort:        "low",
		SystemPrompts: []string{"Be brief."},
		MaxTurns:      3,
		ResumeID:      "test-session",
	}

	// This will fail (no claude binary) but exercises the args building code path.
	t.Setenv("PATH", t.TempDir())
	_, err := p.Invoke(context.Background(), "test", params, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
