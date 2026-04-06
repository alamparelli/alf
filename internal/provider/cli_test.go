package provider

import (
	"context"
	"testing"
	"time"
)

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
