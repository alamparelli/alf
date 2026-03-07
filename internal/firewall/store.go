package firewall

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Store handles persistence of firewall config.
type Store struct {
	path string
}

// NewStore creates a Store for the given config directory.
func NewStore(configDir string) *Store {
	return &Store{path: filepath.Join(configDir, "firewall.json")}
}

// Load reads the config from disk; returns default if file doesn't exist.
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes the config to disk.
func (s *Store) Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}
