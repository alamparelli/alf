package tooling

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ToolSchema describes a tool's interface for OpenAI function calling.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// Registry discovers and holds tool schemas from JSON manifests.
type Registry struct {
	schemas map[string]ToolSchema
	dataDir string
}

// NewRegistry scans tools.d/*.json and tools/*.json under dataDir for tool manifests.
func NewRegistry(dataDir string) *Registry {
	r := &Registry{
		schemas: make(map[string]ToolSchema),
		dataDir: dataDir,
	}
	r.scan()
	return r
}

func (r *Registry) scan() {
	dirs := []string{
		filepath.Join(r.dataDir, "tools.d"),
		filepath.Join(r.dataDir, "tools"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				log.Printf("tooling: failed to read %s: %v", path, err)
				continue
			}
			var schema ToolSchema
			if err := json.Unmarshal(data, &schema); err != nil {
				log.Printf("tooling: failed to parse %s: %v", path, err)
				continue
			}
			if schema.Name == "" {
				schema.Name = strings.TrimSuffix(e.Name(), ".json")
			}
			r.schemas[schema.Name] = schema
		}
	}
	if len(r.schemas) > 0 {
		names := make([]string, 0, len(r.schemas))
		for n := range r.schemas {
			names = append(names, n)
		}
		log.Printf("tooling: loaded %d tool schemas: %v", len(r.schemas), names)
	}
}

// ForTools returns schemas for the named tools. Tools without a JSON manifest
// get a generic fallback schema.
func (r *Registry) ForTools(names []string) []ToolSchema {
	var result []ToolSchema
	for _, name := range names {
		if s, ok := r.schemas[name]; ok {
			result = append(result, s)
		} else {
			result = append(result, fallbackSchema(name))
		}
	}
	return result
}

// Get returns a single tool schema by name, or false if not found.
func (r *Registry) Get(name string) (ToolSchema, bool) {
	s, ok := r.schemas[name]
	return s, ok
}

// RegisterNative adds a native Go tool's schema to the registry.
func (r *Registry) RegisterNative(t NativeTool) {
	r.schemas[t.ToolName()] = t.Schema()
}

// AllSchemas returns all registered schemas (native + file-based user tools).
// Use this to build the tool list for API LLMs.
func (r *Registry) AllSchemas() []ToolSchema {
	schemas := make([]ToolSchema, 0, len(r.schemas))
	for _, s := range r.schemas {
		schemas = append(schemas, s)
	}
	return schemas
}

// UserToolNames returns only user tool names from tools/ (not system/native tools).
// Use this to generate the toolbox for Claude CLI.
func (r *Registry) UserToolNames() []string {
	entries, err := os.ReadDir(filepath.Join(r.dataDir, "tools"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	return names
}

// signalTools are CLI-only tools that require ALF_SIGNAL_SOCK (unix socket
// from the daemon). They must not be exposed to API/OpenRouter tiers.
var signalTools = map[string]bool{
	"react":  true,
	"status": true,
}

// DiscoverToolNames returns all executable tool names found in tools.d/ and tools/.
// This includes tools with and without JSON manifests.
// Signal tools (react, status) are excluded because they require ALF_SIGNAL_SOCK
// which is only available in CLI subprocess context.
func DiscoverToolNames(dataDir string) []string {
	seen := make(map[string]bool)
	for _, dir := range []string{
		filepath.Join(dataDir, "tools.d"),
		filepath.Join(dataDir, "tools"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			if signalTools[e.Name()] {
				continue
			}
			seen[e.Name()] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	return names
}

func fallbackSchema(name string) ToolSchema {
	return ToolSchema{
		Name:        name,
		Description: "Run the " + name + " tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Command-line arguments",
				},
			},
			"required":             []string{"args"},
			"additionalProperties": false,
		},
	}
}

// SanitizeToolName replaces characters not allowed by Anthropic's API
// (pattern: ^[a-zA-Z0-9_]{1,64}$) with underscores.
func SanitizeToolName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, name)
}

// ToOpenAI converts tool schemas to the OpenAI function calling format.
// Tool names are sanitized for Anthropic API compatibility.
func ToOpenAI(schemas []ToolSchema) []map[string]any {
	tools := make([]map[string]any, len(schemas))
	for i, s := range schemas {
		params := s.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        SanitizeToolName(s.Name),
				"description": s.Description,
				"parameters":  params,
				"strict":      true,
			},
		}
	}
	return tools
}
