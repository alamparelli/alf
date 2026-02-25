package controlcenter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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

func TestFileConfigStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	store := NewFileConfigStore(path)

	cfg := &Config{
		LogLevel:       "debug",
		Model:          "opus",
		AllowedChatIDs: []int64{123, 456},
		SystemPrompt:   "test prompt",
		QuietHours:     QuietHours{Start: 22, End: 7},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

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

func TestFileConfigStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	store := NewFileConfigStore(path)

	cfg := DefaultConfig()
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// tmp file should not exist after successful save
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file should be cleaned up after save")
	}

	// File should be valid JSON
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	var check Config
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
}

func TestFileConfigStore_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	store := NewFileConfigStore(path)

	cfg := DefaultConfig()
	if err := store.Save(cfg); err != nil {
		t.Fatalf("initial Save() error: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			store.Load()
		}()
		go func() {
			defer wg.Done()
			store.Save(cfg)
		}()
	}
	wg.Wait()
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{LogLevel: "info", Model: "sonnet"}, false},
		{"empty is valid", Config{}, false},
		{"bad log level", Config{LogLevel: "verbose"}, true},
		{"bad model", Config{Model: "gpt4"}, true},
		{"bad quiet start", Config{QuietHours: QuietHours{Start: 25}}, true},
		{"bad quiet end", Config{QuietHours: QuietHours{End: -1}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(&tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfigJSON_UnknownKeys(t *testing.T) {
	valid := `{"log_level":"info","model":"sonnet"}`
	if err := ValidateConfigJSON([]byte(valid)); err != nil {
		t.Errorf("valid JSON rejected: %v", err)
	}

	invalid := `{"log_level":"info","unknown_key":"val"}`
	if err := ValidateConfigJSON([]byte(invalid)); err == nil {
		t.Error("unknown key should be rejected")
	}
}
