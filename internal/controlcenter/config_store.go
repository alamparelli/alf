package controlcenter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// fileConfigStore implements ConfigStore with atomic file writes and a RWMutex.
type fileConfigStore struct {
	path string
	mu   sync.RWMutex
}

// NewFileConfigStore creates a ConfigStore backed by a JSON file.
func NewFileConfigStore(path string) ConfigStore {
	return &fileConfigStore{path: path}
}

func (s *fileConfigStore) Load() (*Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func (s *fileConfigStore) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	// Atomic write: write to tmp file then rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// ValidateConfig checks that cfg has valid field values.
func ValidateConfig(cfg *Config) error {
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if cfg.LogLevel != "" && !validLevels[cfg.LogLevel] {
		return fmt.Errorf("invalid log_level %q", cfg.LogLevel)
	}
	if cfg.Model != "" && !AllowedModels[cfg.Model] {
		return fmt.Errorf("invalid model %q", cfg.Model)
	}
	if cfg.QuietHours.Start < 0 || cfg.QuietHours.Start > 23 {
		return fmt.Errorf("quiet_hours.start must be 0-23")
	}
	if cfg.QuietHours.End < 0 || cfg.QuietHours.End > 23 {
		return fmt.Errorf("quiet_hours.end must be 0-23")
	}
	return nil
}

// ValidateConfigJSON rejects unknown top-level keys.
func ValidateConfigJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	known := map[string]bool{
		"log_level":        true,
		"model":            true,
		"allowed_chat_ids": true,
		"system_prompt":    true,
		"quiet_hours":      true,
	}
	for key := range raw {
		if !known[key] {
			return fmt.Errorf("unknown config key %q", key)
		}
	}
	return nil
}

// ConfigPath returns the standard config.json path for a data directory.
func ConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "config.json")
}
