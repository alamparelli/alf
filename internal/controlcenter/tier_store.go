package controlcenter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// fileTierStore implements TierStore with hot-reload support.
type fileTierStore struct {
	path    string
	mu      sync.RWMutex
	current atomic.Pointer[TiersConfig]
}

// NewFileTierStore creates a TierStore backed by a JSON file.
func NewFileTierStore(path string) TierStore {
	s := &fileTierStore{path: path}
	s.current.Store(DefaultTiersConfig())
	return s
}

func (s *fileTierStore) Load() (*TiersConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultTiersConfig(), nil
		}
		return nil, fmt.Errorf("read tiers: %w", err)
	}

	var tiers TiersConfig
	if err := json.Unmarshal(data, &tiers); err != nil {
		return nil, fmt.Errorf("parse tiers: %w", err)
	}
	return &tiers, nil
}

func (s *fileTierStore) Current() *TiersConfig {
	return s.current.Load()
}

func (s *fileTierStore) Save(cfg *TiersConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tiers: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create tiers dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tiers tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename tiers: %w", err)
	}

	s.current.Store(cfg)
	return nil
}

func (s *fileTierStore) Reload() error {
	tiers, err := s.Load()
	if err != nil {
		return err
	}
	s.current.Store(tiers)
	return nil
}

// TiersPath returns the standard tiers.json path for a data directory.
func TiersPath(dataDir string) string {
	return filepath.Join(dataDir, "config", "tiers.json")
}
