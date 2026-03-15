package controlcenter

import (
	"encoding/json"
	"net/http"

	"github.com/alamparelli/alf/internal/tooling"
)

// TiersHandler serves the full tiers configuration for the CC tiers tab.
type TiersHandler struct {
	TierStore    TierStore
	Notifier     Notifier
	DataDir      string             // for tool discovery
	ToolRegistry *tooling.Registry  // may be nil
	ModelCache   *ModelCache        // may be nil - pre-fetched models per backend
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
		}
		backends := make([]string, 0, len(AllowedBackends))
		for b := range AllowedBackends {
			if b != "" {
				backends = append(backends, b)
			}
		}
		// Build tool list: CLI tools + ALF tools.
		tools := append([]toolInfo{}, cliTools...)
		if h.ToolRegistry != nil {
			// Native Go tools + user tools with JSON schemas - curated list only.
			// Only tools with a JSON schema - LLMs need a definition to invoke them.
			for _, schema := range h.ToolRegistry.AllSchemas() {
				tools = append(tools, toolInfo{Name: schema.Name, Desc: schema.Description, Source: "alf"})
			}
		} else if h.DataDir != "" {
			// Fallback when no registry: only show tools with a JSON schema.
			for _, schema := range tooling.NewRegistry(h.DataDir).AllSchemas() {
				tools = append(tools, toolInfo{Name: schema.Name, Desc: schema.Description, Source: "alf"})
			}
		}
		// Include pre-fetched models per backend (populated by background cache).
		var backendModels map[string][]modelInfo
		if h.ModelCache != nil {
			backendModels = h.ModelCache.All()
		}
		respondJSON(w, http.StatusOK, tiersResponse{TiersConfig: cfg, AvailableBackends: backends, AvailableTools: tools, BackendModels: backendModels})

	case http.MethodPut:
		var cfg TiersConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := validateTiersConfig(&cfg); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := h.TierStore.Save(&cfg); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
			return
		}
		if h.Notifier != nil {
			h.Notifier.Notify(ReloadTiers)
		}
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		methodNotAllowed(w)
	}
}

func validateTiersConfig(cfg *TiersConfig) error {
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
		if !isAPIBackend && !AllowedModels[t.Model] {
			return errVal("invalid model for tier " + t.Name + ": " + t.Model)
		}
		if t.Effort != "" && !AllowedEfforts[t.Effort] {
			return errVal("invalid effort for tier " + t.Name + ": " + t.Effort)
		}
		if !AllowedBackends[t.Backend] {
			return errVal("invalid backend for tier " + t.Name + ": " + t.Backend)
		}
	}
	isAPIRouter := cfg.RouterBackend != "" && cfg.RouterBackend != "cli"
	if cfg.RouterModel != "" && !isAPIRouter && !AllowedModels[cfg.RouterModel] {
		return errVal("invalid router_model: " + cfg.RouterModel)
	}
	if !AllowedBackends[cfg.RouterBackend] {
		return errVal("invalid router_backend: " + cfg.RouterBackend)
	}
	return nil
}

type valError struct{ msg string }

func (e *valError) Error() string { return e.msg }
func errVal(msg string) error    { return &valError{msg: msg} }
