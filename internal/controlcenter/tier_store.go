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
	pathVal atomic.Value // stores string - use Path()/SetPath() accessors
	mu      sync.RWMutex
	current atomic.Pointer[TiersConfig]
}

// NewFileTierStore creates a TierStore backed by a JSON file.
func NewFileTierStore(path string) TierStore {
	s := &fileTierStore{}
	s.pathVal.Store(path)
	s.current.Store(DefaultTiersConfig())
	return s
}

// Path returns the current backing file path.
func (s *fileTierStore) Path() string {
	return s.pathVal.Load().(string)
}

// SetPath changes the backing file and reloads tiers from the new path.
// Returns an error if the new file exists but cannot be parsed.
func (s *fileTierStore) SetPath(path string) error {
	s.pathVal.Store(path)
	return s.Reload()
}

func (s *fileTierStore) Load() (*TiersConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.Path())
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

	path := s.Path()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tiers: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create tiers dir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tiers tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
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

// TiersPath returns the standard tiers.json path for a config directory.
func TiersPath(configDir string) string {
	return filepath.Join(configDir, "tiers.json")
}

// TiersPathFromConfig returns the tiers file path from Config.TiersFile.
// Relative paths are resolved against configDir; absolute paths are used as-is.
// Falls back to TiersPath when cfg is nil or TiersFile is empty (e.g. old config.json
// written before this field existed).
func TiersPathFromConfig(configDir string, cfg *Config) string {
	if cfg == nil || cfg.TiersFile == "" {
		return TiersPath(configDir)
	}
	if filepath.IsAbs(cfg.TiersFile) {
		return cfg.TiersFile
	}
	return filepath.Join(configDir, cfg.TiersFile)
}
