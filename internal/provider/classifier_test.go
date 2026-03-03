package provider

import (
	"testing"
	"time"
)

func TestNewCLIClassifier_Defaults(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		SystemPrompt: "You are a router.",
		DataDir:      t.TempDir(),
	})
	if c.cfg.IdleTimeout != 30*time.Minute {
		t.Errorf("expected default idle timeout 30m, got %v", c.cfg.IdleTimeout)
	}
	if c.cfg.MaxRetries != 3 {
		t.Errorf("expected default max retries 3, got %d", c.cfg.MaxRetries)
	}
}

func TestCLIClassifier_NotReadyBeforeStart(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		SystemPrompt: "You are a router.",
		DataDir:      t.TempDir(),
	})
	if c.IsReady() {
		t.Error("classifier should not be ready before Start()")
	}
}

func TestCLIClassifier_StartFailsNoClaude(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		SystemPrompt: "You are a router.",
		DataDir:      t.TempDir(),
	})
	t.Setenv("PATH", t.TempDir())

	err := c.Start()
	if err == nil {
		t.Fatal("expected error when claude binary not found")
	}
	if c.IsReady() {
		t.Error("classifier should not be ready after failed start")
	}
}

func TestCLIClassifier_CloseIdempotent(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		SystemPrompt: "test",
		DataDir:      t.TempDir(),
	})
	// Close without start should not panic.
	if err := c.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
	// Double close should be safe.
	if err := c.Close(); err != nil {
		t.Errorf("second Close error: %v", err)
	}
}

func TestCLIClassifier_RestartFailsNoClaude(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		SystemPrompt: "test",
		DataDir:      t.TempDir(),
	})
	t.Setenv("PATH", t.TempDir())

	err := c.Restart()
	if err == nil {
		t.Fatal("expected error on restart with no claude binary")
	}
}

func TestCLIClassifier_UpdateModel(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		Model:        "claude-haiku-4-5",
		SystemPrompt: "test",
		DataDir:      t.TempDir(),
	})
	t.Setenv("PATH", t.TempDir())

	// UpdateModel changes the model even if restart fails.
	_ = c.UpdateModel("claude-sonnet-4-6")
	if c.cfg.Model != "claude-sonnet-4-6" {
		t.Errorf("expected model update to claude-sonnet-4-6, got %q", c.cfg.Model)
	}
}

func TestCLIClassifier_UpdateSystemPrompt(t *testing.T) {
	c := NewCLIClassifier(ClassifierConfig{
		SystemPrompt: "old prompt",
		DataDir:      t.TempDir(),
	})
	t.Setenv("PATH", t.TempDir())

	_ = c.UpdateSystemPrompt("new prompt")
	if c.cfg.SystemPrompt != "new prompt" {
		t.Errorf("expected prompt update, got %q", c.cfg.SystemPrompt)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"ab", 2, "ab"},
		{"abc", 2, "ab..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}
