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

func (s *fileTierStore) Save(tiers *TiersConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(tiers, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tiers: %w", err)
	}
	data = append(data, '\n')

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tiers tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename tiers: %w", err)
	}

	// Swap in-memory pointer after successful write.
	s.current.Store(tiers)
	return nil
}

func (s *fileTierStore) Current() *TiersConfig {
	return s.current.Load()
}

func (s *fileTierStore) Reload() error {
	tiers, err := s.Load()
	if err != nil {
		return err
	}
	s.current.Store(tiers)
	return nil
}

// ValidateTiers checks that all tiers have valid fields.
func ValidateTiers(tiers *TiersConfig) error {
	for i, t := range tiers.Tiers {
		if t.Name == "" {
			return fmt.Errorf("tier %d: name is required", i)
		}
		if !AllowedModels[t.Model] {
			return fmt.Errorf("tier %d (%s): invalid model %q", i, t.Name, t.Model)
		}
	}
	return nil
}

// TiersPath returns the standard tiers.json path for a data directory.
func TiersPath(dataDir string) string {
	return filepath.Join(dataDir, "tiers.json")
}
