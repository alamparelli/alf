package controlcenter

import (
	"path/filepath"
	"testing"
)

func TestFileConfigStore_LoadDefault(t *testing.T) {
	dir := t.TempDir()
	store := NewFileConfigStore(filepath.Join(dir, "config.json"))

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default log_level 'info', got %q", cfg.LogLevel)
	}
	if cfg.Model != "sonnet" {
		t.Errorf("expected default model 'sonnet', got %q", cfg.Model)
	}
}

func TestFileConfigStore_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write config directly to disk (simulating user/CLI edit).
	writeJSON(t, path, &Config{
		LogLevel:       "debug",
		Model:          "opus",
		AllowedChatIDs: []int64{123, 456},
		SystemPrompt:   "test prompt",
		QuietHours:     QuietHours{Start: 22, End: 7},
	})

	store := NewFileConfigStore(path)
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.LogLevel != "debug" {
		t.Errorf("log_level: got %q, want 'debug'", loaded.LogLevel)
	}
	if loaded.Model != "opus" {
		t.Errorf("model: got %q, want 'opus'", loaded.Model)
	}
	if len(loaded.AllowedChatIDs) != 2 {
		t.Errorf("allowed_chat_ids: got %d items, want 2", len(loaded.AllowedChatIDs))
	}
	if loaded.QuietHours.Start != 22 {
		t.Errorf("quiet_hours.start: got %d, want 22", loaded.QuietHours.Start)
	}
}

func TestFileConfigStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	store := NewFileConfigStore(path)

	cfg := &Config{
		LogLevel:         "debug",
		Model:            "opus",
		AllowedChatIDs:   []int64{42},
		SystemPrompt:     "be helpful",
		QuietHours:       QuietHours{Start: 22, End: 7},
		SessionTimeout:   60,
		GitTrack:         true,
		GitSweepInterval: 10,
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Model != "opus" {
		t.Errorf("model: got %q, want 'opus'", loaded.Model)
	}
	if loaded.GitTrack != true {
		t.Error("git_track should be true")
	}
	if loaded.GitSweepInterval != 10 {
		t.Errorf("git_sweep_interval: got %d, want 10", loaded.GitSweepInterval)
	}
	if loaded.SessionTimeout != 60 {
		t.Errorf("session_timeout: got %d, want 60", loaded.SessionTimeout)
	}
}

func TestFileConfigStore_ConcurrentRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeJSON(t, path, DefaultConfig())
	store := NewFileConfigStore(path)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			store.Load()
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
