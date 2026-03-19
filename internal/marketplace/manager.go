package marketplace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type AppState string

const (
	StateInstalled AppState = "installed"
	StateEnabled   AppState = "enabled"
	StateDisabled  AppState = "disabled"
)

type AppInfo struct {
	Manifest
	State AppState `json:"state"`
}

// RemoteApp represents an app available on the remote registry.
type RemoteApp struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
}

type Manager struct {
	dataDir     string
	registryURL string // e.g. http://alf-marketplace:8090
	mu          sync.Mutex
	states      map[string]AppState
	onChange    func()
	http       *http.Client
}

type stateFile struct {
	States map[string]AppState `json:"states"`
}

func NewManager(dataDir string) *Manager {
	m := &Manager{
		dataDir:     dataDir,
		registryURL: os.Getenv("ALF_MARKETPLACE_URL"),
		states:      make(map[string]AppState),
		http:        &http.Client{Timeout: 30 * time.Second},
	}
	m.loadState()
	return m
}

func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

func (m *Manager) List() []AppInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	appsDir := filepath.Join(m.dataDir, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil
	}

	var apps []AppInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		manifestPath := filepath.Join(appsDir, slug, "manifest.json")
		manifest, err := LoadManifest(manifestPath)
		if err != nil {
			continue
		}

		state, ok := m.states[slug]
		if !ok {
			state = StateInstalled
		}

		apps = append(apps, AppInfo{
			Manifest: *manifest,
			State:    state,
		})
	}

	return apps
}

func (m *Manager) Enable(slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	manifest, err := m.loadManifest(slug)
	if err != nil {
		return err
	}

	toolsDir := filepath.Join(m.dataDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}

	for _, tool := range manifest.Tools {
		symlinkPath := filepath.Join(toolsDir, tool.Name)
		target := filepath.Join("..", "apps", slug, "bin", slug)

		// Remove existing symlink if any.
		os.Remove(symlinkPath)
		if err := os.Symlink(target, symlinkPath); err != nil {
			return fmt.Errorf("symlink %s: %w", tool.Name, err)
		}

		if err := m.writeToolSchema(toolsDir, tool); err != nil {
			return fmt.Errorf("schema %s: %w", tool.Name, err)
		}
	}

	// Link bundled skills: apps/<slug>/skills/<name>/ → data/skills/<name>/
	m.linkAppSkills(slug)

	m.states[slug] = StateEnabled
	if err := m.saveState(); err != nil {
		return err
	}

	if m.onChange != nil {
		m.onChange()
	}

	return nil
}

func (m *Manager) Disable(slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	manifest, err := m.loadManifest(slug)
	if err != nil {
		return err
	}

	toolsDir := filepath.Join(m.dataDir, "tools")
	for _, tool := range manifest.Tools {
		os.Remove(filepath.Join(toolsDir, tool.Name))
		os.Remove(filepath.Join(toolsDir, tool.Name+".json"))
	}

	// Unlink bundled skills.
	m.unlinkAppSkills(slug)

	m.states[slug] = StateDisabled
	if err := m.saveState(); err != nil {
		return err
	}

	if m.onChange != nil {
		m.onChange()
	}

	return nil
}

func (m *Manager) Uninstall(slug string) error {
	// Disable first (removes symlinks + schemas).
	if err := m.Disable(slug); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	appDir := filepath.Join(m.dataDir, "apps", slug)

	// Remove everything except data/ (user data never deleted).
	entries, _ := os.ReadDir(appDir)
	for _, e := range entries {
		if e.Name() == "data" {
			continue
		}
		os.RemoveAll(filepath.Join(appDir, e.Name()))
	}

	delete(m.states, slug)

	return m.saveState()
}

func (m *Manager) RestoreEnabled() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	toolsDir := filepath.Join(m.dataDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}

	for slug, state := range m.states {
		if state != StateEnabled {
			continue
		}

		manifest, err := m.loadManifest(slug)
		if err != nil {
			continue
		}

		for _, tool := range manifest.Tools {
			symlinkPath := filepath.Join(toolsDir, tool.Name)
			target := filepath.Join("..", "apps", slug, "bin", slug)

			os.Remove(symlinkPath)
			if err := os.Symlink(target, symlinkPath); err != nil {
				continue
			}

			m.writeToolSchema(toolsDir, tool)
		}
	}

	return nil
}

// linkAppSkills symlinks skill directories from apps/<slug>/skills/ into data/skills/.
func (m *Manager) linkAppSkills(slug string) {
	skillsSrc := filepath.Join(m.dataDir, "apps", slug, "skills")
	entries, err := os.ReadDir(skillsSrc)
	if err != nil {
		return // no skills directory
	}
	skillsDst := filepath.Join(m.dataDir, "skills")
	os.MkdirAll(skillsDst, 0o755)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		link := filepath.Join(skillsDst, e.Name())
		target := filepath.Join("..", "apps", slug, "skills", e.Name())
		os.Remove(link) // remove stale
		os.Symlink(target, link)
	}
}

// unlinkAppSkills removes skill symlinks that point to this app.
func (m *Manager) unlinkAppSkills(slug string) {
	skillsSrc := filepath.Join(m.dataDir, "apps", slug, "skills")
	entries, err := os.ReadDir(skillsSrc)
	if err != nil {
		return
	}
	skillsDst := filepath.Join(m.dataDir, "skills")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		os.Remove(filepath.Join(skillsDst, e.Name()))
	}
}

func (m *Manager) loadManifest(slug string) (*Manifest, error) {
	path := filepath.Join(m.dataDir, "apps", slug, "manifest.json")
	return LoadManifest(path)
}

func (m *Manager) writeToolSchema(toolsDir string, tool ToolDecl) error {
	params := tool.Parameters
	if params == nil {
		params = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
				},
			},
			"required": []any{"action"},
		}
	}

	schema := map[string]any{
		"name":        tool.Name,
		"description": tool.Description,
		"parameters":  params,
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	return os.WriteFile(filepath.Join(toolsDir, tool.Name+".json"), data, 0o644)
}

func (m *Manager) loadState() {
	path := filepath.Join(m.dataDir, "apps", ".state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return
	}

	if sf.States != nil {
		m.states = sf.States
	}
}

func (m *Manager) saveState() error {
	appsDir := filepath.Join(m.dataDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return fmt.Errorf("create apps dir: %w", err)
	}

	sf := stateFile{States: m.states}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	path := filepath.Join(appsDir, ".state.json")
	return os.WriteFile(path, data, 0o644)
}

// --- Remote registry ---

// FetchCatalog returns the list of apps available on the remote registry.
// Returns nil if no registry URL is configured.
func (m *Manager) FetchCatalog() ([]RemoteApp, error) {
	if m.registryURL == "" {
		return nil, nil
	}
	req, err := http.NewRequest("GET", m.registryURL+"/api/catalog", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Alf-Instance", "true")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("catalog: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB max
	if err != nil {
		return nil, err
	}
	var apps []RemoteApp
	if err := json.Unmarshal(body, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

// Install downloads an app from the remote registry and installs it locally.
// Downloads: manifest, binary (for current arch), web assets.
func (m *Manager) Install(slug string) error {
	if m.registryURL == "" {
		return fmt.Errorf("no registry configured")
	}

	appDir := filepath.Join(m.dataDir, "apps", slug)
	os.MkdirAll(appDir, 0o755)

	// Download manifest.
	if err := m.downloadFile(slug, "manifest", filepath.Join(appDir, "manifest.json")); err != nil {
		return fmt.Errorf("download manifest: %w", err)
	}

	// Download binary for current arch (optional — web-only apps have no binary).
	arch := runtime.GOARCH
	binDir := filepath.Join(appDir, "bin")
	os.MkdirAll(binDir, 0o755)
	binPath := filepath.Join(binDir, slug)
	if err := m.downloadBinary(slug, arch, binPath); err != nil {
		// Binary is optional — web-only apps don't have one.
		os.Remove(binPath)
	} else {
		os.Chmod(binPath, 0o755)
	}

	// Download web assets (index.html, app.json).
	webFiles := []string{"index.html", "app.json"}
	for _, f := range webFiles {
		dst := filepath.Join(appDir, f)
		if err := m.downloadWebAsset(slug, f, dst); err != nil {
			continue
		}
	}

	// Download bundled skills.
	m.downloadSkills(slug, appDir)

	m.mu.Lock()
	m.states[slug] = StateInstalled
	m.saveState()
	m.mu.Unlock()

	return nil
}

func (m *Manager) downloadFile(slug, endpoint, dst string) error {
	url := fmt.Sprintf("%s/api/apps/%s/%s", m.registryURL, slug, endpoint)
	return m.httpGet(url, dst)
}

func (m *Manager) downloadBinary(slug, arch, dst string) error {
	url := fmt.Sprintf("%s/api/apps/%s/download?arch=%s", m.registryURL, slug, arch)
	return m.httpGet(url, dst)
}

// downloadSkills fetches the skill list from the registry and downloads each skill's files.
func (m *Manager) downloadSkills(slug, appDir string) {
	if m.registryURL == "" {
		return
	}
	url := fmt.Sprintf("%s/api/apps/%s/skills", m.registryURL, slug)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Alf-Instance", "true")
	resp, err := m.http.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	type skillInfo struct {
		Name  string   `json:"name"`
		Files []string `json:"files"`
	}
	var skills []skillInfo
	if json.Unmarshal(body, &skills) != nil || len(skills) == 0 {
		return
	}

	for _, sk := range skills {
		skillDir := filepath.Join(appDir, "skills", sk.Name)
		os.MkdirAll(skillDir, 0o755)
		for _, f := range sk.Files {
			dst := filepath.Join(skillDir, f)
			fileURL := fmt.Sprintf("%s/api/apps/%s/skill/%s/%s", m.registryURL, slug, sk.Name, f)
			_ = m.httpGet(fileURL, dst)
		}
	}
}

func (m *Manager) downloadWebAsset(slug, file, dst string) error {
	url := fmt.Sprintf("%s/api/apps/%s/web/%s", m.registryURL, slug, file)
	return m.httpGet(url, dst)
}

func (m *Manager) httpGet(url, dst string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Alf-Instance", "true")
	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, 256<<20)) // 256MB max
	return err
}

// --- Auto-update ---

// UpdateInfo describes an app with a newer version available remotely.
type UpdateInfo struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	LocalVersion  string `json:"local_version"`
	RemoteVersion string `json:"remote_version"`
}

// CheckUpdates compares local app versions against the remote catalog.
// Returns a list of apps that have a newer version available.
func (m *Manager) CheckUpdates() []UpdateInfo {
	if m.registryURL == "" {
		return nil
	}

	catalog, err := m.FetchCatalog()
	if err != nil || len(catalog) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var updates []UpdateInfo
	for _, remote := range catalog {
		state, ok := m.states[remote.Slug]
		if !ok {
			continue
		}
		// Only check installed or enabled apps, skip disabled/available.
		if state != StateInstalled && state != StateEnabled {
			continue
		}

		manifest, err := m.loadManifest(remote.Slug)
		if err != nil {
			continue
		}

		if remote.Version > manifest.Version {
			updates = append(updates, UpdateInfo{
				Slug:          remote.Slug,
				Name:          remote.Name,
				LocalVersion:  manifest.Version,
				RemoteVersion: remote.Version,
			})
		}
	}

	return updates
}

// Update re-downloads an app from the registry, preserving its data/ directory
// and current state (enabled/installed).
func (m *Manager) Update(slug string) error {
	if m.registryURL == "" {
		return fmt.Errorf("no registry configured")
	}

	m.mu.Lock()
	prevState, hasState := m.states[slug]
	m.mu.Unlock()

	if !hasState {
		return fmt.Errorf("app %q is not installed", slug)
	}

	appDir := filepath.Join(m.dataDir, "apps", slug)

	// Remove everything except data/ (preserve user data).
	entries, err := os.ReadDir(appDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read app dir: %w", err)
	}
	for _, e := range entries {
		if e.Name() == "data" {
			continue
		}
		os.RemoveAll(filepath.Join(appDir, e.Name()))
	}

	// Re-download from registry (same logic as Install).
	os.MkdirAll(appDir, 0o755)

	if err := m.downloadFile(slug, "manifest", filepath.Join(appDir, "manifest.json")); err != nil {
		return fmt.Errorf("download manifest: %w", err)
	}

	arch := runtime.GOARCH
	binDir := filepath.Join(appDir, "bin")
	os.MkdirAll(binDir, 0o755)
	binPath := filepath.Join(binDir, slug)
	if err := m.downloadBinary(slug, arch, binPath); err != nil {
		os.Remove(binPath)
	} else {
		os.Chmod(binPath, 0o755)
	}

	webFiles := []string{"index.html", "app.json"}
	for _, f := range webFiles {
		dst := filepath.Join(appDir, f)
		if err := m.downloadWebAsset(slug, f, dst); err != nil {
			continue
		}
	}

	// Restore previous state.
	m.mu.Lock()
	m.states[slug] = prevState
	m.saveState()
	m.mu.Unlock()

	// If the app was enabled, re-enable to refresh symlinks/schemas.
	if prevState == StateEnabled {
		return m.Enable(slug)
	}

	return nil
}
