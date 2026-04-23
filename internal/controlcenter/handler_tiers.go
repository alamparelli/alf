package controlcenter

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alamparelli/alf/internal/tooling"
)

// TiersHandler serves the full tiers configuration for the CC tiers tab.
type TiersHandler struct {
	TierStore    TierStore
	Notifier     Notifier
	ToolRegistry *tooling.Registry // may be nil — when nil the "available_tools" list falls back to cliTools only
	ModelCache   *ModelCache       // may be nil - pre-fetched models per backend
	EventBroker  *EventBroker
}

// toolInfo describes an available tool for the frontend.
type toolInfo struct {
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Source string `json:"source"` // "cli" or "alf"
}

// cliTools are the Claude Code built-in tools.
var cliTools = []toolInfo{
	{Name: "Read", Desc: "Read files (code, config, logs, images, PDF)", Source: "cli"},
	{Name: "Write", Desc: "Create or overwrite files", Source: "cli"},
	{Name: "Edit", Desc: "Modify existing files (text replacement)", Source: "cli"},
	{Name: "Bash", Desc: "Execute shell commands", Source: "cli"},
	{Name: "Glob", Desc: "Search files by pattern (e.g. **/*.go)", Source: "cli"},
	{Name: "Grep", Desc: "Search file contents with regex", Source: "cli"},
	{Name: "WebSearch", Desc: "Search the web for information", Source: "cli"},
	{Name: "WebFetch", Desc: "Fetch content from a URL", Source: "cli"},
	{Name: "NotebookEdit", Desc: "Edit Jupyter notebooks", Source: "cli"},
	{Name: "Agent", Desc: "Launch a sub-agent for complex tasks", Source: "cli"},
}

func (h *TiersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := h.TierStore.Current()
		// Include registered backends so frontend can populate dropdowns.
		type tiersResponse struct {
			*TiersConfig
			AvailableBackends []string                `json:"available_backends"`
			AvailableTools    []toolInfo              `json:"available_tools"`
			BackendModels     map[string][]modelInfo  `json:"backend_models,omitempty"`
			ProviderSchemas   []ProviderSchema         `json:"provider_schemas"`
		}
		backends := make([]string, 0, len(AllowedBackends))
		for b := range AllowedBackends {
			if b != "" {
				backends = append(backends, b)
			}
		}
		// Build tool list: CLI tools + ALF tools. ToolRegistry is wired by the
		// daemon bootstrap (cmd/alf-daemon/main.go) through factory.go; when it
		// is unexpectedly nil the tab shows only the CLI subset rather than
		// triggering a fresh disk scan on every request.
		tools := append([]toolInfo{}, cliTools...)
		if h.ToolRegistry != nil {
			for _, schema := range h.ToolRegistry.AllSchemas() {
				tools = append(tools, toolInfo{Name: schema.Name, Desc: schema.Description, Source: "alf"})
			}
		}
		// Include pre-fetched models per backend (populated by background cache).
		var backendModels map[string][]modelInfo
		if h.ModelCache != nil {
			backendModels = h.ModelCache.All()
		}
		// Build provider schemas annotated with configured status.
		providerSchemas := AnnotateConfigured(AllProviderSchemas(), backends)
		respondJSON(w, http.StatusOK, tiersResponse{TiersConfig: cfg, AvailableBackends: backends, AvailableTools: tools, BackendModels: backendModels, ProviderSchemas: providerSchemas})

	case http.MethodPut:
		var cfg TiersConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON: " + err.Error())
			return
		}
		if err := validateTiersConfig(&cfg); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.TierStore.Save(&cfg); err != nil {
			respondError(w, http.StatusInternalServerError, "save failed: " + err.Error())
			return
		}
		notifyReload(h.Notifier, ReloadTiers)
		h.EventBroker.Emit(EventTiers)
		respondOK(w)

	default:
		methodNotAllowed(w)
	}
}

func validateTiersConfig(cfg *TiersConfig) error {
	knownProviders := KnownProviderIDs()
	names := map[string]bool{}
	for _, t := range cfg.Tiers {
		if t.Name == "" {
			return errVal("tier name is required")
		}
		if names[t.Name] {
			return errVal("duplicate tier name: " + t.Name)
		}
		names[t.Name] = true
		// Skip model validation for API backends (any model ID is valid).
		isAPIBackend := t.Backend != "" && t.Backend != "cli"
		if !isAPIBackend && !IsValidClaudeModel(t.Model) {
			return errVal("invalid model for tier " + t.Name + ": " + t.Model)
		}
		if t.Effort != "" && !AllowedEfforts[t.Effort] {
			return errVal("invalid effort for tier " + t.Name + ": " + t.Effort)
		}
		// Accept registered backends AND known provider schema IDs.
		if !AllowedBackends[t.Backend] && !knownProviders[t.Backend] {
			return errVal("invalid backend for tier " + t.Name + ": " + t.Backend)
		}
	}
	// Validate fallback references and detect cycles.
	for _, t := range cfg.Tiers {
		if t.Fallback != "" {
			if !names[t.Fallback] {
				return errVal("tier " + t.Name + ": fallback references unknown tier: " + t.Fallback)
			}
			if t.Fallback == t.Name {
				return errVal("tier " + t.Name + ": fallback cannot reference itself")
			}
		}
	}
	// Detect cycles in fallback chains.
	tierMap := make(map[string]string, len(cfg.Tiers))
	for _, t := range cfg.Tiers {
		tierMap[t.Name] = t.Fallback
	}
	for _, t := range cfg.Tiers {
		if t.Fallback == "" {
			continue
		}
		visited := map[string]bool{t.Name: true}
		cur := t.Fallback
		for cur != "" {
			if visited[cur] {
				return errVal("fallback cycle detected involving tier: " + t.Name)
			}
			visited[cur] = true
			cur = tierMap[cur]
		}
	}

	isAPIRouter := cfg.RouterBackend != "" && cfg.RouterBackend != "cli"
	if cfg.RouterModel != "" && !isAPIRouter && !IsValidClaudeModel(cfg.RouterModel) {
		return errVal("invalid router_model: " + cfg.RouterModel)
	}
	if !AllowedBackends[cfg.RouterBackend] && !knownProviders[cfg.RouterBackend] {
		return errVal("invalid router_backend: " + cfg.RouterBackend)
	}
	return nil
}

type valError struct{ msg string }

func (e *valError) Error() string { return e.msg }
func errVal(msg string) error    { return &valError{msg: msg} }

// TierConfigsHandler manages tier configuration files in config.d/tiers/.
// GET  /api/tiers/configs        — list available configs
// POST /api/tiers/configs/switch  — switch active config
type TierConfigsHandler struct {
	ConfigDir   string
	TierStore   TierStore
	ConfigStore ConfigStore
	Notifier    Notifier
	EventBroker *EventBroker
}

type tierConfigEntry struct {
	Name   string `json:"name"`   // filename without .json
	Path   string `json:"path"`   // relative path from configDir
	Active bool   `json:"active"` // true if currently loaded
	Tiers  int    `json:"tiers"`  // number of tiers in this config
}

func (h *TierConfigsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tiers/configs")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		h.handleList(w, r)
	case path == "switch" && r.Method == http.MethodPost:
		h.handleSwitch(w, r)
	case path == "duplicate" && r.Method == http.MethodPost:
		h.handleDuplicate(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *TierConfigsHandler) handleList(w http.ResponseWriter, _ *http.Request) {
	tiersDir := filepath.Join(h.ConfigDir, "tiers")
	entries, err := os.ReadDir(tiersDir)
	if err != nil {
		if os.IsNotExist(err) {
			respondJSON(w, http.StatusOK, []tierConfigEntry{})
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	activePath := h.TierStore.Path()
	var configs []tierConfigEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(tiersDir, e.Name())
		name := strings.TrimSuffix(e.Name(), ".json")
		relPath := filepath.Join("tiers", e.Name())

		// Count tiers in this config.
		tierCount := 0
		if data, err := os.ReadFile(fullPath); err == nil {
			var tc TiersConfig
			if json.Unmarshal(data, &tc) == nil {
				tierCount = len(tc.Tiers)
			}
		}

		configs = append(configs, tierConfigEntry{
			Name:   name,
			Path:   relPath,
			Active: fullPath == activePath,
			Tiers:  tierCount,
		})
	}
	respondJSON(w, http.StatusOK, configs)
}

func (h *TierConfigsHandler) handleSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"` // filename, e.g. "grok.json"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		respondError(w, http.StatusBadRequest, "name required")
		return
	}

	// Validate: no path traversal, must be a simple filename.
	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "..") || !strings.HasSuffix(req.Name, ".json") {
		respondError(w, http.StatusBadRequest, "invalid name")
		return
	}

	fullPath := filepath.Join(h.ConfigDir, "tiers", req.Name)
	if _, err := os.Stat(fullPath); err != nil {
		respondError(w, http.StatusNotFound, "config not found")
		return
	}

	// Update tiers_file in config.json (just the filename, resolved via tiers/ subdir).
	cfg, err := h.ConfigStore.Load()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "load config: " + err.Error())
		return
	}
	cfg.TiersFile = req.Name
	if err := h.ConfigStore.Save(cfg); err != nil {
		respondError(w, http.StatusInternalServerError, "save config: " + err.Error())
		return
	}

	// Switch the tier store to the new path.
	if err := h.TierStore.SetPath(fullPath); err != nil {
		respondError(w, http.StatusInternalServerError, "reload tiers: " + err.Error())
		return
	}

	notifyReload(h.Notifier, ReloadTiers, ReloadConfig)
	h.EventBroker.Emit(EventTiers)
	h.EventBroker.Emit(EventConfig)

	log.Printf("[tiers] switched to %s", req.Name)
	respondOK(w)
}

func (h *TierConfigsHandler) handleDuplicate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"` // e.g. "claude.json"
		Name   string `json:"name"`   // e.g. "claude-copy.json"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Source == "" || req.Name == "" {
		respondError(w, http.StatusBadRequest, "source and name required")
		return
	}
	for _, n := range []string{req.Source, req.Name} {
		if strings.Contains(n, "/") || strings.Contains(n, "..") || !strings.HasSuffix(n, ".json") {
			respondError(w, http.StatusBadRequest, "invalid name")
			return
		}
	}

	tiersDir := filepath.Join(h.ConfigDir, "tiers")
	srcPath := filepath.Join(tiersDir, req.Source)
	dstPath := filepath.Join(tiersDir, req.Name)

	if _, err := os.Stat(srcPath); err != nil {
		respondError(w, http.StatusNotFound, "source not found")
		return
	}
	if _, err := os.Stat(dstPath); err == nil {
		respondError(w, http.StatusConflict, "name already exists")
		return
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "read source: " + err.Error())
		return
	}
	if err := os.WriteFile(dstPath, data, 0o644); err != nil {
		respondError(w, http.StatusInternalServerError, "write copy: " + err.Error())
		return
	}

	log.Printf("[tiers] duplicated %s → %s", req.Source, req.Name)
	respondOK(w)
}
