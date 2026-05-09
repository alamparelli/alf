package marketplace

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// validSkillName restricts bundled skill directory names to the same
// shape as the resource-store validName regex ([a-zA-Z0-9_-]+). The
// outer bundle extraction rejects path-traversal prefixes, but the
// directory name itself is exposed to the LLM and the UI — weird
// unicode, spaces or shell metacharacters have no business there.
// Kept local to avoid an import cycle on controlcenter (#385-6).
var validSkillName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// errInsecureRegistry is returned by validateRegistryURL when the URL uses
// a non-https scheme without the ALF_MARKETPLACE_INSECURE=1 dev override.
var errInsecureRegistry = errors.New("insecure registry URL (http://) requires ALF_MARKETPLACE_INSECURE=1")

// validateRegistryURL enforces HTTPS on the marketplace registry URL.
//
//   - raw == "":       returns "" (marketplace disabled, not an error).
//   - https://…:       accepted.
//   - http://… + insecure=="1": accepted, caller should warn.
//   - http://… no override:     rejected (errInsecureRegistry).
//   - unparseable/other scheme: rejected.
//
// Returning the cleaned URL lets callers normalise trailing slashes in future.
func validateRegistryURL(raw, insecure string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse registry URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("registry URL missing host: %q", raw)
	}
	switch u.Scheme {
	case "https":
		return raw, nil
	case "http":
		if insecure != "1" {
			return "", errInsecureRegistry
		}
		return raw, nil
	default:
		return "", fmt.Errorf("registry URL has unsupported scheme %q (want https)", u.Scheme)
	}
}

type AppState string

const (
	StateInstalled AppState = "installed"
	StateEnabled   AppState = "enabled"
	StateDisabled  AppState = "disabled"
)

// isActive returns true if the app state represents a running/active app.
func (s AppState) isActive() bool {
	return s == StateInstalled || s == StateEnabled
}

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

// AppSupervisor is the interface for starting/stopping app services.
type AppSupervisor interface {
	StartApp(slug string)
	StopApp(slug string)
	RestartApp(slug string)
}

type Manager struct {
	dataDir     string
	registryURL string // e.g. http://alf-marketplace:8090
	mu          sync.Mutex
	states      map[string]AppState
	perms       map[string][]string // slug → declared permissions (nil = all allowed)
	services    map[string][]string // slug → declared vault services
	trusted     map[string]bool     // slug → true ONLY for MarkTrusted (built-in apps); marketplace installs are untrusted (#384)
	onChange    func()
	supervisor  AppSupervisor
	http        *http.Client

	// trustStore is the daemon's shared envelope trust store. Wired
	// post-construction via SetTrustStore so the marketplace verify
	// path (#384) and the WASM loader share the same set of public
	// keys (auto-bootstrapped daemon key + operator-imported third
	// parties + alf-marketplace pubkey when embedded). Install / Update
	// refuse to run if this is nil — there is no fallback to the
	// pre-#384 unsigned-bundle path.
	trustStore envelope.TrustStore
}

type stateFile struct {
	States  map[string]AppState `json:"states"`
	Trusted map[string]bool     `json:"trusted,omitempty"` // slugs installed from registry
}

func NewManager(dataDir string) *Manager {
	rawURL := os.Getenv("ALF_MARKETPLACE_URL")
	insecure := os.Getenv("ALF_MARKETPLACE_INSECURE")
	registryURL, err := validateRegistryURL(rawURL, insecure)
	if err != nil {
		log.Printf("[marketplace] rejecting ALF_MARKETPLACE_URL=%q: %v — marketplace disabled. Set ALF_MARKETPLACE_INSECURE=1 to allow plain http:// (dev only).", rawURL, err)
	} else if registryURL != "" && insecure == "1" {
		log.Printf("[marketplace] WARNING: registry URL %q is plain http:// (ALF_MARKETPLACE_INSECURE=1). Do not use this configuration in production.", registryURL)
	}

	m := &Manager{
		dataDir:     dataDir,
		registryURL: registryURL,
		states:      make(map[string]AppState),
		perms:       make(map[string][]string),
		services:    make(map[string][]string),
		trusted:     make(map[string]bool),
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

func (m *Manager) SetSupervisor(sv AppSupervisor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.supervisor = sv
}

// HasPermission checks if an app has the given permission.
// Returns true if:
//   - app has no permissions field in manifest (backward compat: all allowed)
//   - app is not tracked by the marketplace (internal app: all allowed)
//   - the permission is explicitly listed
func (m *Manager) HasPermission(slug, perm string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	perms, tracked := m.perms[slug]
	if !tracked {
		return true // not in cache = no restrictions (internal or legacy app)
	}
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// IsTracked returns true if the app is managed by the marketplace.
// Internal/default apps (like "developer") are not tracked and should
// bypass sandboxing since they are trusted platform components.
func (m *Manager) IsTracked(slug string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, tracked := m.perms[slug]
	return tracked
}

// MarkTrusted marks a slug as trusted (e.g. bundled/default apps).
// Trusted apps get their full declared permissions without capping.
//
// Marketplace-installed apps do NOT pass through this method any
// more (#384): the "trusted = came-from-registry" heuristic is
// gone. A marketplace install passes envelope.Verify against a key
// in the trust store; that's the install gate. The runtime perm
// ceiling (Tier-2 for daemon-key, Tier-3 user-endorsed for wider
// scopes) is independent — see MANIFEST-SCHEMA §5.
func (m *Manager) MarkTrusted(slug string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trusted[slug] = true
}

// SetTrustStore wires the daemon's shared envelope.TrustStore into
// the manager. Required before Install / Update can run. Daemon
// boot calls this after setupWASMLoader has constructed the store
// so every consumer (WASM loader, skills loader, marketplace) sees
// the same set of trusted publishers — including the embedded
// daemon key, the embedded alf-marketplace key (when present), and
// any operator-imported third-party keys under <dataDir>/trust/.
func (m *Manager) SetTrustStore(s envelope.TrustStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trustStore = s
}

// GetServices returns the declared vault services for an app.
// Returns nil if the app has no vault service declarations.
func (m *Manager) GetServices(slug string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	svcs := m.services[slug]
	if svcs == nil {
		return nil
	}
	result := make([]string, len(svcs))
	copy(result, svcs)
	return result
}

// GetPermissions returns the declared permissions for an app.
// Returns nil if the app has no restrictions (all allowed).
func (m *Manager) GetPermissions(slug string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	perms, tracked := m.perms[slug]
	if !tracked {
		return nil // all allowed
	}
	result := make([]string, len(perms))
	copy(result, perms)
	return result
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
			continue // local app — not marketplace-managed
		}

		// Set trust from manager state (not manifest — SEC-001)
		manifest.Trusted = m.trusted[slug]

		apps = append(apps, AppInfo{
			Manifest: *manifest,
			State:    state,
		})
	}

	return apps
}

// activate sets up tool symlinks, skills, permissions and starts the app service.
// Must be called with m.mu held.
func (m *Manager) activate(slug string) error {
	manifest, err := m.loadManifest(slug)
	if err != nil {
		return err
	}

	// Server-side manifest validation safety net.
	if errs, _ := ValidateManifest(manifest); len(errs) > 0 {
		return fmt.Errorf("invalid manifest: %s", errs[0])
	}

	toolsDir := filepath.Join(m.dataDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}

	// Resolve the CLI tool binary (bin/{slug} or bin/{arch}/{slug}).
	// Do NOT fall back to "server" — that's the HTTP service, not the CLI tool.
	toolBinRel := m.resolveToolBinary(slug)

	for _, tool := range manifest.Tools {
		if toolBinRel == "" {
			log.Printf("marketplace: [%s] tool %q skipped — no CLI binary found in bin/", slug, tool.Name)
			continue
		}

		symlinkPath := filepath.Join(toolsDir, tool.Name)
		target := filepath.Join("..", "apps", slug, toolBinRel)

		// Ensure the binary is executable.
		absTarget := filepath.Join(m.dataDir, "apps", slug, toolBinRel)
		os.Chmod(absTarget, 0o755)

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

	// Lock app files (read-only) to prevent LLM from modifying marketplace apps.
	// Only lock if this is a registry-installed app (has a remote entry in state).
	if m.registryURL != "" {
		m.lockAppFiles(slug)
	}

	// Cache declared permissions.
	// SEC-002: Untrusted apps with nil permissions get safe defaults, not "all allowed".
	isTrusted := m.trusted[slug]
	perms := manifest.Permissions
	if !isTrusted {
		if perms == nil {
			// Untrusted app with no permissions field → safe defaults only
			perms = []string{"storage", "events", "clipboard"}
		} else {
			perms = CapPermissionsForUntrusted(perms)
		}
	}
	if perms != nil {
		m.perms[slug] = perms
	} else {
		delete(m.perms, slug) // trusted app, no restrictions
	}

	// Cache declared vault services.
	if len(manifest.Services) > 0 {
		m.services[slug] = manifest.Services
	} else {
		delete(m.services, slug)
	}

	// Start the app's service if it has one.
	if m.supervisor != nil {
		m.supervisor.StartApp(slug)
	}

	return nil
}

// deactivate tears down tool symlinks, skills, permissions and stops the app service.
// Caller must NOT hold m.mu (permissions cleared under separate lock for SEC-008).
func (m *Manager) deactivate(slug string) error {
	// SEC-008: Clear permissions first (under lock) to prevent use during teardown.
	m.mu.Lock()
	delete(m.perms, slug)
	m.mu.Unlock()

	// Stop the app's service before further cleanup.
	if m.supervisor != nil {
		m.supervisor.StopApp(slug)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Unlock files first so they can be cleaned up.
	m.unlockAppFiles(slug)

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

	return nil
}

// resolveToolBinary finds the CLI tool binary for an app (bin/{slug} or bin/{arch}/{slug}).
// Returns empty string if no CLI binary found — never falls back to "server".
func (m *Manager) resolveToolBinary(slug string) string {
	appDir := filepath.Join(m.dataDir, "apps", slug)
	// Preferred: bin/{slug}
	if fi, err := os.Stat(filepath.Join(appDir, "bin", slug)); err == nil && !fi.IsDir() {
		return filepath.Join("bin", slug)
	}
	// Bundle format: bin/{arch}/{slug}
	archBin := filepath.Join("bin", runtime.GOARCH, slug)
	if fi, err := os.Stat(filepath.Join(appDir, archBin)); err == nil && !fi.IsDir() {
		return archBin
	}
	return "" // no CLI binary available
}

func (m *Manager) Uninstall(slug string) error {
	// Best-effort graceful deactivation: uses the manifest to unlink tools/skills
	// and stops the service. If the manifest is missing or corrupt we must not
	// abort here — the forceCleanupAppFiles fallback below still handles cleanup
	// using filesystem truth (see issues #250, #277).
	if err := m.deactivate(slug); err != nil {
		log.Printf("marketplace: deactivate %s during uninstall: %v (continuing with fallback cleanup)", slug, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Safety net: scrub any residual tool symlinks, tool schemas, or skill
	// symlinks that still point at this app. Catches the case where deactivate
	// couldn't enumerate them (missing manifest) or where an earlier install
	// crashed partway through.
	m.forceCleanupAppFiles(slug)

	appDir := filepath.Join(m.dataDir, "apps", slug)

	// Remove everything except data/ (user data never deleted).
	entries, _ := os.ReadDir(appDir)
	hasData := false
	for _, e := range entries {
		if e.Name() == "data" {
			hasData = true
			continue
		}
		os.RemoveAll(filepath.Join(appDir, e.Name()))
	}

	// If no user data was preserved, remove the now-empty app directory itself.
	// Without this, leftover empty dirs pollute the Developer Source App dropdown
	// and the marketplace List (issue #277).
	if !hasData {
		os.Remove(appDir)
	}

	delete(m.states, slug)
	delete(m.perms, slug)
	delete(m.services, slug)
	delete(m.trusted, slug)

	if err := m.saveState(); err != nil {
		return err
	}

	if m.onChange != nil {
		m.onChange()
	}

	return nil
}

// forceCleanupAppFiles scrubs residual symlinks and schemas that belong to slug
// using filesystem truth rather than the manifest. Used as a safety net during
// Uninstall so a broken/missing manifest cannot leave orphan files behind
// (issue #250). Must be called with m.mu held.
func (m *Manager) forceCleanupAppFiles(slug string) {
	appFragment := filepath.Join("apps", slug) // "apps/<slug>"

	// Tools: walk data/tools/ and drop anything whose symlink target points
	// into this app directory. Also drop the matching <name>.json schema.
	toolsDir := filepath.Join(m.dataDir, "tools")
	if entries, err := os.ReadDir(toolsDir); err == nil {
		for _, e := range entries {
			full := filepath.Join(toolsDir, e.Name())
			target, lerr := os.Readlink(full)
			if lerr != nil {
				continue // not a symlink — leave it alone
			}
			if !strings.Contains(target, appFragment) {
				continue
			}
			os.Remove(full)
			// Matching schema lives next to the binary symlink.
			os.Remove(full + ".json")
		}
	}

	// Skills: same treatment for data/skills/ entries.
	skillsDir := filepath.Join(m.dataDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			full := filepath.Join(skillsDir, e.Name())
			target, lerr := os.Readlink(full)
			if lerr != nil {
				continue
			}
			if strings.Contains(target, appFragment) {
				os.Remove(full)
			}
		}
	}
}

// RestoreInstalled re-creates tool symlinks and permission caches on daemon startup
// for all installed apps.
func (m *Manager) RestoreInstalled() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	toolsDir := filepath.Join(m.dataDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}

	for slug, state := range m.states {
		if !state.isActive() {
			continue
		}

		manifest, err := m.loadManifest(slug)
		if err != nil {
			// SEC-007: fail-closed — deny all permissions if manifest can't be loaded
			m.perms[slug] = []string{}
			continue
		}

		// Restore permission cache (cap for untrusted apps)
		isTrusted := m.trusted[slug]
		perms := manifest.Permissions
		if !isTrusted {
			if perms == nil {
				perms = []string{"storage", "events", "clipboard"}
			} else {
				perms = CapPermissionsForUntrusted(perms)
			}
		}
		if perms != nil {
			m.perms[slug] = perms
		}

		// Restore vault services cache.
		if len(manifest.Services) > 0 {
			m.services[slug] = manifest.Services
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
// Skill directory names must match validSkillName — any other shape is
// skipped with a log. Extraction already blocks traversal, but a filesystem
// post-extraction or a non-regex-safe unicode name still has to be gated
// because the name is later exposed to the LLM and UI.
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
		name := e.Name()
		if !validSkillName.MatchString(name) {
			log.Printf("[marketplace] app %q: skipping skill %q (invalid name, expected [a-zA-Z0-9_-]+)", slug, name)
			continue
		}
		link := filepath.Join(skillsDst, name)
		target := filepath.Join("..", "apps", slug, "skills", name)
		os.Remove(link) // remove stale
		os.Symlink(target, link)
	}
}

// unlinkAppSkills removes skill symlinks that point to this app.
// Only names matching validSkillName are considered — an invalid name
// was never symlinked by linkAppSkills, so we must not touch whatever
// file may happen to sit at that path.
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
		name := e.Name()
		if !validSkillName.MatchString(name) {
			continue
		}
		os.Remove(filepath.Join(skillsDst, name))
	}
}

// lockAppFiles makes app files read-only to prevent LLM modifications.
// Skips the data/ subdirectory (user data stays writable).
func (m *Manager) lockAppFiles(slug string) {
	appDir := filepath.Join(m.dataDir, "apps", slug)

	// Ensure data/ exists and stays writable before locking everything else.
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)

	filepath.Walk(appDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip data/ — user data must stay writable.
		rel, _ := filepath.Rel(appDir, path)
		if rel == "data" || strings.HasPrefix(rel, "data/") || strings.HasPrefix(rel, "data"+string(filepath.Separator)) {
			return nil
		}
		if info.IsDir() {
			os.Chmod(path, 0o555)
		} else if info.Mode()&0o111 != 0 {
			os.Chmod(path, 0o555) // preserve executable bit
		} else {
			os.Chmod(path, 0o444)
		}
		return nil
	})
}

// unlockAppFiles restores write permissions on app files.
func (m *Manager) unlockAppFiles(slug string) {
	appDir := filepath.Join(m.dataDir, "apps", slug)
	filepath.Walk(appDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			os.Chmod(path, 0o755)
		} else {
			os.Chmod(path, 0o644)
		}
		return nil
	})
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
	if sf.Trusted != nil {
		m.trusted = sf.Trusted
	}
}

func (m *Manager) saveState() error {
	appsDir := filepath.Join(m.dataDir, "apps")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return fmt.Errorf("create apps dir: %w", err)
	}

	sf := stateFile{States: m.states, Trusted: m.trusted}
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

// Install downloads an app from the remote registry, verifies its
// signed envelope against the trust store, and installs it locally
// only on successful verification. The legacy multi-file fallback
// has been retired — under #384 every marketplace install must
// chain to a trusted publisher key.
func (m *Manager) Install(slug string) error {
	if m.registryURL == "" {
		return fmt.Errorf("no registry configured")
	}

	appDir := filepath.Join(m.dataDir, "apps", slug)

	// Unlock existing files in case of reinstall over a locked app.
	m.unlockAppFiles(slug)

	os.MkdirAll(appDir, 0o755)

	// Verified bundle download — refuses unsigned, untrusted-key, or
	// tampered bundles before any file is written to disk.
	if err := m.downloadAndExtractBundle(slug, appDir); err != nil {
		return err
	}

	m.mu.Lock()
	m.states[slug] = StateInstalled
	// trusted=true is reserved for built-in apps via MarkTrusted (#384).
	// Marketplace installs are untrusted; the signer-chain check is the
	// install gate, the per-tier permission ceiling is independent.

	// Activate immediately: create symlinks, permissions, start service.
	if err := m.activate(slug); err != nil {
		m.saveState()
		m.mu.Unlock()
		return err
	}

	if err := m.saveState(); err != nil {
		m.mu.Unlock()
		return err
	}

	if m.onChange != nil {
		m.onChange()
	}

	m.mu.Unlock()
	return nil
}

// MaxBundleSize bounds the bundle.zip download (200 MiB). Anything
// larger is refused before the download completes.
const MaxBundleSize int64 = 200 << 20

// downloadAndExtractBundle downloads a signed ZIP bundle from the
// registry, verifies its envelope against the trust store, then
// extracts. The verify step happens BEFORE any file is written to
// disk — a tampered or unsigned bundle never touches the apps dir.
//
// Wire-level contract (#384):
//
//   GET <registryURL>/api/apps/<slug>/bundle?arch=<arch>   → bundle.zip
//   GET <registryURL>/api/apps/<slug>/bundle.sig?arch=<arch> → bundle.sig
//
// Both responses are required. A 404 on bundle.sig surfaces as
// ErrBundleSignatureMissing so the operator sees "registry has not
// yet been upgraded for v0.8.0 signed bundles" rather than a
// confusing transport error.
//
// Failure modes the caller can branch on:
//   - errors.Is(err, ErrBundleSignatureMissing): server upgrade pending.
//   - errors.Is(err, envelope.ErrSignerNotTrusted): unknown publisher.
//   - errors.Is(err, ErrBundleManifestNotMarketplace): wrong kind.
//   - errors.Is(err, ErrBundleManifestMissing): legacy unsigned bundle.
//   - any other err: transport / canonicalisation / signature math.
func (m *Manager) downloadAndExtractBundle(slug, appDir string) error {
	if m.trustStore == nil {
		return fmt.Errorf("marketplace: trust store not wired — daemon boot must call SetTrustStore before Install")
	}

	bundleBytes, err := m.fetchBundleArtefact(slug, "bundle", MaxBundleSize, false)
	if err != nil {
		return fmt.Errorf("download bundle: %w", err)
	}
	sigBytes, err := m.fetchBundleArtefact(slug, "bundle.sig", MaxBundleSignatureSize, true)
	if err != nil {
		return err // already typed as ErrBundleSignatureMissing on 404
	}

	if _, err := verifyBundle(bundleBytes, sigBytes, m.trustStore); err != nil {
		return fmt.Errorf("verify bundle: %w", err)
	}

	// Verify passed — now extract from the in-memory bytes via
	// *bytes.Reader (implements io.ReaderAt + size). No disk
	// round-trip; the bundle bytes already live in RAM since we
	// needed them for the verify hash check.
	if err := extractBundle(bytes.NewReader(bundleBytes), int64(len(bundleBytes)), appDir); err != nil {
		return err
	}
	if err := ensureServiceBinExecutable(appDir); err != nil {
		log.Printf("marketplace: chmod service binary for %s: %v", slug, err)
	}
	return nil
}

// fetchBundleArtefact issues a GET against the registry and returns
// the response body bounded by maxSize. When sigArtefact is true a
// 404 is mapped to ErrBundleSignatureMissing; for the bundle itself
// we keep the raw HTTP error so the caller distinguishes "wrong
// slug" (404) from "registry not upgraded" (404 on .sig).
func (m *Manager) fetchBundleArtefact(slug, kind string, maxSize int64, sigArtefact bool) ([]byte, error) {
	url := fmt.Sprintf("%s/api/apps/%s/%s?arch=%s", m.registryURL, slug, kind, runtime.GOARCH)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Alf-Instance", "true")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 && sigArtefact {
		return nil, ErrBundleSignatureMissing
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

// ensureServiceBinExecutable reads service.json and chmod +x's the declared command.
// This is a safety net for bundles packed without the executable bit on the binary.
func ensureServiceBinExecutable(appDir string) error {
	data, err := os.ReadFile(filepath.Join(appDir, "service.json"))
	if err != nil {
		return nil // no service.json, nothing to do
	}
	var svc struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(data, &svc); err != nil || svc.Command == "" {
		return nil
	}
	binPath := svc.Command
	if !filepath.IsAbs(binPath) {
		binPath = filepath.Join(appDir, binPath)
	}
	binPath = filepath.Clean(binPath)
	// Security: binary must stay within appDir.
	if !strings.HasPrefix(binPath, filepath.Clean(appDir)+string(filepath.Separator)) {
		return fmt.Errorf("service command escapes app directory: %s", svc.Command)
	}
	return os.Chmod(binPath, 0o755)
}

// extractBundle extracts a ZIP into appDir, skipping data/ entries.
func extractBundle(ra io.ReaderAt, size int64, appDir string) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}

	const maxFiles = 100
	const maxFileSize int64 = 50 << 20 // 50MB
	count := 0

	for _, zf := range zr.File {
		// Security: reject path traversal and absolute paths.
		clean := filepath.Clean(zf.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}

		// Skip data/ directory (preserve user data).
		if clean == "data" || strings.HasPrefix(clean, "data/") || strings.HasPrefix(clean, "data"+string(filepath.Separator)) {
			continue
		}

		target := filepath.Join(appDir, clean)

		// Verify target stays within appDir.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(appDir)+string(filepath.Separator)) && target != filepath.Clean(appDir) {
			continue
		}

		if zf.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}

		count++
		if count > maxFiles {
			return fmt.Errorf("too many files in bundle (max %d)", maxFiles)
		}

		os.MkdirAll(filepath.Dir(target), 0o755)

		mode := os.FileMode(0o644)
		if zf.Mode()&0o111 != 0 || strings.HasPrefix(clean, "bin/") || clean == "server" {
			mode = 0o755 // preserve executable bit; force for binaries
		}

		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return fmt.Errorf("create %s: %w", clean, err)
		}

		rc, err := zf.Open()
		if err != nil {
			f.Close()
			return fmt.Errorf("open %s: %w", clean, err)
		}
		_, copyErr := io.Copy(f, io.LimitReader(rc, maxFileSize))
		rc.Close()
		f.Close()
		if copyErr != nil {
			return fmt.Errorf("write %s: %w", clean, copyErr)
		}
	}

	return nil
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
		if !state.isActive() {
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

// Update re-downloads an app from the registry, preserving its data/ directory.
func (m *Manager) Update(slug string) error {
	if m.registryURL == "" {
		return fmt.Errorf("no registry configured")
	}

	m.mu.Lock()
	_, hasState := m.states[slug]
	m.mu.Unlock()

	if !hasState {
		return fmt.Errorf("app %q is not installed", slug)
	}

	// Deactivate before updating files.
	if err := m.deactivate(slug); err != nil {
		return err
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

	// Re-download from registry: signed-bundle path only (#384).
	os.MkdirAll(appDir, 0o755)

	if err := m.downloadAndExtractBundle(slug, appDir); err != nil {
		return err
	}

	// Re-activate: restore symlinks, permissions, start service.
	m.mu.Lock()
	m.states[slug] = StateInstalled
	if err := m.activate(slug); err != nil {
		m.saveState()
		m.mu.Unlock()
		return err
	}
	m.saveState()
	if m.onChange != nil {
		m.onChange()
	}
	m.mu.Unlock()

	return nil
}
