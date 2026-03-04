package controlcenter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// ResourceMeta describes a stored resource file.
type ResourceMeta struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// ResourceStore provides CRUD for named resource files.
type ResourceStore interface {
	List() ([]ResourceMeta, error)
	Get(name string) ([]byte, error)
	Put(name string, data []byte) error
	Delete(name string) error
}

// maxResourceSize is the maximum size for a resource file (1 MB).
const maxResourceSize = 1 << 20

// validName matches safe resource names: alphanumeric, dashes, underscores.
var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// fileResourceStore implements ResourceStore backed by a directory.
type fileResourceStore struct {
	dir     string
	ext     string // e.g. ".md" or ".json"
	maxSize int    // max file size in bytes (0 = use default maxResourceSize)
	mu      sync.RWMutex
}

// NewFileResourceStore creates a ResourceStore in dir with the given file extension.
// The directory is created if it doesn't exist.
func NewFileResourceStore(dir, ext string) ResourceStore {
	os.MkdirAll(dir, 0o775)
	return &fileResourceStore{dir: dir, ext: ext}
}

// NewFileResourceStoreWithLimit creates a ResourceStore with a custom max file size.
func NewFileResourceStoreWithLimit(dir, ext string, maxSize int) ResourceStore {
	os.MkdirAll(dir, 0o775)
	return &fileResourceStore{dir: dir, ext: ext, maxSize: maxSize}
}

func (s *fileResourceStore) validateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid resource name %q: must match [a-zA-Z0-9_-]", name)
	}
	return nil
}

func (s *fileResourceStore) path(name string) string {
	return filepath.Join(s.dir, name+s.ext)
}

func (s *fileResourceStore) List() ([]ResourceMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ResourceMeta{}, nil
		}
		return nil, fmt.Errorf("read resource dir: %w", err)
	}

	var items []ResourceMeta
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != s.ext {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()[:len(e.Name())-len(s.ext)]
		items = append(items, ResourceMeta{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return items, nil
}

func (s *fileResourceStore) Get(name string) ([]byte, error) {
	if err := s.validateName(name); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("resource %q not found", name)
		}
		return nil, fmt.Errorf("read resource: %w", err)
	}
	return data, nil
}

func (s *fileResourceStore) Put(name string, data []byte) error {
	if err := s.validateName(name); err != nil {
		return err
	}
	limit := maxResourceSize
	if s.maxSize > 0 {
		limit = s.maxSize
	}
	if len(data) > limit {
		return fmt.Errorf("resource too large: %d bytes (max %d)", len(data), limit)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Atomic write.
	p := s.path(name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o664); err != nil {
		return fmt.Errorf("write resource tmp: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename resource: %w", err)
	}
	return nil
}

func (s *fileResourceStore) Delete(name string) error {
	if err := s.validateName(name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path(name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("resource %q not found", name)
		}
		return fmt.Errorf("delete resource: %w", err)
	}
	return nil
}
