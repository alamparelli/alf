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
}

func TestFileConfigStore_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write config directly to disk (simulating user/CLI edit).
	writeJSON(t, path, &Config{
		LogLevel:       "debug",
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

func TestConfigPath(t *testing.T) {
	got := ConfigPath("/opt/alf/config.d")
	want := "/opt/alf/config.d/config.json"
	if got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestFileConfigStore_SaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	// Path inside a non-existent subdirectory.
	path := filepath.Join(dir, "config.d", "config.json")
	store := NewFileConfigStore(path)

	cfg := DefaultConfig()
	cfg.LogLevel = "warn"
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() should create parent dir: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after Save: %v", err)
	}
	if loaded.LogLevel != "warn" {
		t.Errorf("log_level: got %q, want 'warn'", loaded.LogLevel)
	}
}

func TestDefaultConfig_NotificationSoundTrue(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.NotificationSound == nil {
		t.Fatal("DefaultConfig().NotificationSound should not be nil")
	}
	if *cfg.NotificationSound != true {
		t.Error("DefaultConfig().NotificationSound should be true")
	}
}

func TestFileConfigStore_NotificationSound_JSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	store := NewFileConfigStore(path)

	cfg := DefaultConfig()
	*cfg.NotificationSound = false

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.NotificationSound == nil {
		t.Fatal("NotificationSound should not be nil after round-trip")
	}
	if *loaded.NotificationSound != false {
		t.Error("NotificationSound should be false after round-trip")
	}
}

func TestFileConfigStore_AutoMigrate_NotificationSound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write an old config without notification_sound field.
	writeJSON(t, path, map[string]any{
		"log_level":        "info",
		"allowed_chat_ids": []int64{},
		"system_prompt":    "",
		"quiet_hours":      map[string]int{"start": 0, "end": 0},
		"session_timeout":  30,
		"git_track":        true,
		"git_sweep_interval": 15,
	})

	store := NewFileConfigStore(path)
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Auto-migration should fill in the default (true).
	if loaded.NotificationSound == nil {
		t.Fatal("NotificationSound should be set after auto-migration")
	}
	if *loaded.NotificationSound != true {
		t.Error("NotificationSound should default to true after auto-migration")
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
