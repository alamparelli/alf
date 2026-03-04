package controlcenter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// fileConfigStore implements ConfigStore with a RWMutex.
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
	data, err := os.ReadFile(s.path)
	s.mu.RUnlock()

	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := *DefaultConfig() // start with defaults so new fields get sane values
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Write back if file is missing new fields (auto-migrate on load).
	merged, _ := json.MarshalIndent(&cfg, "", "  ")
	if string(merged) != string(data) {
		_ = s.Save(&cfg)
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

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// ConfigPath returns the standard config.json path for a config directory.
func ConfigPath(configDir string) string {
	return filepath.Join(configDir, "config.json")
}
