package controlcenter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AppMeta describes an installed app directory.
type AppMeta struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	ModTime     string `json:"mod_time"`
}

// appJSON is the on-disk format of app.json inside each app folder.
type appJSON struct {
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// AppStore provides read access to directory-based apps.
type AppStore interface {
	List() ([]AppMeta, error)
	// ReadFile returns the contents of a file within an app directory.
	// path is relative to the app root, e.g. "index.html" or "assets/style.css".
	ReadFile(app, path string) ([]byte, error)
}

// fileAppStore implements AppStore backed by a directory of app folders.
type fileAppStore struct {
	dir string
	mu  sync.RWMutex
}

// NewFileAppStore creates an AppStore scanning the given directory for app folders.
func NewFileAppStore(dir string) AppStore {
	os.MkdirAll(dir, 0o775)
	return &fileAppStore{dir: dir}
}

func (s *fileAppStore) List() ([]AppMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []AppMeta{}, nil
		}
		return nil, fmt.Errorf("read apps dir: %w", err)
	}

	// Load marketplace state to filter disabled apps.
	disabled := loadDisabledApps(s.dir)

	var apps []AppMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()

		// Skip disabled apps (marketplace state).
		if disabled[name] {
			continue
		}

		// Must have index.html to be a valid app.
		indexPath := filepath.Join(s.dir, name, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		meta := AppMeta{
			Name:    name,
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		}

		// Try to read app.json for display metadata.
		if data, err := os.ReadFile(filepath.Join(s.dir, name, "app.json")); err == nil {
			var aj appJSON
			if json.Unmarshal(data, &aj) == nil {
				if aj.Name != "" {
					meta.DisplayName = aj.Name
				}
				meta.Icon = aj.Icon
				meta.Description = aj.Description
				meta.Category = aj.Category
			}
		}

		// Fallback: read category from manifest.json if app.json didn't provide one.
		if meta.Category == "" {
			if data, err := os.ReadFile(filepath.Join(s.dir, name, "manifest.json")); err == nil {
				var mf struct {
					Category string `json:"category"`
				}
				if json.Unmarshal(data, &mf) == nil {
					meta.Category = mf.Category
				}
			}
		}

		apps = append(apps, meta)
	}
	return apps, nil
}

// loadDisabledApps reads .state.json and returns a set of disabled app slugs.
func loadDisabledApps(appsDir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(appsDir, ".state.json"))
	if err != nil {
		return nil
	}
	var sf struct {
		States map[string]string `json:"states"`
	}
	if json.Unmarshal(data, &sf) != nil {
		return nil
	}
	disabled := make(map[string]bool)
	for slug, state := range sf.States {
		if state == "disabled" {
			disabled[slug] = true
		}
	}
	return disabled
}

func (s *fileAppStore) ReadFile(app, path string) ([]byte, error) {
	if !validName.MatchString(app) {
		return nil, fmt.Errorf("invalid app name %q", app)
	}

	// Resolve and validate the path stays within the app directory.
	appDir := filepath.Join(s.dir, app)
	target := filepath.Join(appDir, filepath.Clean(path))

	// Prevent path traversal.
	absApp, err := filepath.Abs(appDir)
	if err != nil {
		return nil, fmt.Errorf("resolve app dir: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	if absTarget != absApp && !hasPrefix(absTarget, absApp+string(filepath.Separator)) {
		return nil, fmt.Errorf("path traversal blocked")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(absTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file %q not found in app %q", path, app)
		}
		return nil, fmt.Errorf("read app file: %w", err)
	}
	return data, nil
}

// hasPrefix checks if path starts with prefix (for path containment checks).
func hasPrefix(path, prefix string) bool {
	return len(path) >= len(prefix) && path[:len(prefix)] == prefix
}
