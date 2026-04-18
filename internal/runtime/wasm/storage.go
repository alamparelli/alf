package wasm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Storage is a file-backed KV store scoped to a single capability.
// Keys are hashed to avoid path-escape games.
type Storage struct {
	dir string
}

// NewStorage initializes a per-capability storage under <dataRoot>/<name>/.
func NewStorage(dataRoot, name string) (*Storage, error) {
	dir := filepath.Join(dataRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage mkdir %s: %w", dir, err)
	}
	return &Storage{dir: dir}, nil
}

func (s *Storage) pathFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".bin")
}

func (s *Storage) Put(key string, value []byte) error {
	return os.WriteFile(s.pathFor(key), value, 0o644)
}

func (s *Storage) Get(key string) ([]byte, bool) {
	data, err := os.ReadFile(s.pathFor(key))
	if err != nil {
		return nil, false
	}
	return data, true
}

func (s *Storage) Delete(key string) error {
	err := os.Remove(s.pathFor(key))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
