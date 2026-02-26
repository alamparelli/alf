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

// ConfigPath returns the standard config.json path for a data directory.
func ConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "config.json")
}
