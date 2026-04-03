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
	schemas     map[string]ToolSchema
	natives     map[string]NativeTool
	nativeNames []string
	dataDir     string
	secWarnings []SecurityWarning
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
	r.scanFiles(true)
}

// Rescan re-reads tool schemas from disk (tools.d/*.json, tools/*.json).
// Native Go tools are preserved - only file-based schemas are refreshed.
func (r *Registry) Rescan() {
	r.scanFiles(false)
}

// dangerousPatterns are substrings in tool source code that indicate shell injection risk.
var dangerousPatterns = []struct {
	pattern string
	reason  string
}{
	{"shell=True", "Python subprocess with shell=True allows command injection (CWE-78)"},
	{"os.system(", "os.system() passes commands through the shell (CWE-78)"},
	{"os.popen(", "os.popen() passes commands through the shell (CWE-78)"},
	{"eval(", "eval() executes arbitrary code (CWE-94)"},
}

// SecurityWarning records a dangerous pattern found in a user tool.
type SecurityWarning struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

// SecurityWarnings returns warnings from the last tool scan.
func (r *Registry) SecurityWarnings() []SecurityWarning {
	return r.secWarnings
}

// auditToolSource scans a tool's source code for dangerous patterns.
func auditToolSource(toolPath, toolName string) []SecurityWarning {
	data, err := os.ReadFile(toolPath)
	if err != nil {
		return nil
	}
	src := string(data)
	var warnings []SecurityWarning
	for _, dp := range dangerousPatterns {
		if strings.Contains(src, dp.pattern) {
			warnings = append(warnings, SecurityWarning{
				Tool:    toolName,
				Pattern: dp.pattern,
				Reason:  dp.reason,
			})
		}
	}
	return warnings
}

func (r *Registry) scanFiles(initial bool) {
	// Preserve native tool schemas during rescan.
	nativeSchemas := make(map[string]ToolSchema)
	if !initial {
		for _, name := range r.nativeNames {
			if s, ok := r.schemas[name]; ok {
				nativeSchemas[name] = s
			}
		}
		// Reset to only native schemas.
		r.schemas = nativeSchemas
	}

	systemDir := filepath.Join(r.dataDir, "tools.d")
	userDir := filepath.Join(r.dataDir, "tools")
	dirs := []string{systemDir, userDir}
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
			// Prevent user tools from shadowing system tool schemas.
			if dir == userDir {
				if _, exists := r.schemas[schema.Name]; exists {
					log.Printf("tooling: BLOCKED schema shadow: tools/%s (system tool protected)", e.Name())
					continue
				}
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

	// Audit user tool source files for dangerous patterns (shell injection, eval, etc.).
	r.secWarnings = nil
	userToolDir := filepath.Join(r.dataDir, "tools")
	if entries, err := os.ReadDir(userToolDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(userToolDir, e.Name())
			if warnings := auditToolSource(path, e.Name()); len(warnings) > 0 {
				for _, w := range warnings {
					log.Printf("tooling: ⚠ SECURITY WARNING in tools/%s: %s", w.Tool, w.Reason)
				}
				r.secWarnings = append(r.secWarnings, warnings...)
			}
		}
	}
}

// ForTools returns schemas for the named tools. Tools without a JSON manifest
// get a generic fallback schema (used by CLI tiers where toolbox.md provides context).
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

// ForToolsStrict returns schemas only for tools that have a proper schema
// (JSON manifest or native registration). Tools without schemas are skipped.
// Use this for API tiers where the model has no other context about tools.
func (r *Registry) ForToolsStrict(names []string) []ToolSchema {
	var result []ToolSchema
	var skipped []string
	for _, name := range names {
		if s, ok := r.schemas[name]; ok {
			result = append(result, s)
		} else {
			skipped = append(skipped, name)
		}
	}
	if len(skipped) > 0 {
		log.Printf("tooling: skipped %d tools without schema for API tier: %v", len(skipped), skipped)
	}
	return result
}

// Get returns a single tool schema by name, or false if not found.
func (r *Registry) Get(name string) (ToolSchema, bool) {
	s, ok := r.schemas[name]
	return s, ok
}

// RegisterNative adds a native Go tool's schema and instance to the registry.
func (r *Registry) RegisterNative(t NativeTool) {
	r.schemas[t.ToolName()] = t.Schema()
	if r.natives == nil {
		r.natives = make(map[string]NativeTool)
	}
	r.natives[t.ToolName()] = t
	r.nativeNames = append(r.nativeNames, t.ToolName())
}

// GetNative returns a native tool by name, or nil if not found.
func (r *Registry) GetNative(name string) NativeTool {
	return r.natives[name]
}

// NativeToolNames returns only the names of native Go tools (not user tools).
func (r *Registry) NativeToolNames() []string {
	return r.nativeNames
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

// ResolveWildcard expands a "*" tool wildcard into all CLI + native tools, deduplicated.
// When a tool name exists both as a CLI binary and a native Go tool, native wins (same schema key).
func ResolveWildcard(dataDir string, reg *Registry) []string {
	seen := make(map[string]bool)
	var tools []string
	for _, n := range DiscoverToolNames(dataDir) {
		if !seen[n] {
			seen[n] = true
			tools = append(tools, n)
		}
	}
	if reg != nil {
		for _, n := range reg.NativeToolNames() {
			if !seen[n] {
				seen[n] = true
				tools = append(tools, n)
			}
		}
	}
	return tools
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
// Schemas are patched for strict mode: all properties added to required,
// optional properties made nullable, additionalProperties set to false.
func ToOpenAI(schemas []ToolSchema) []map[string]any {
	return toOpenAI(schemas, true)
}

// ToOpenAICompat converts tool schemas without strict mode.
// Use this for Ollama and other backends that don't support OpenAI strict schemas.
func ToOpenAICompat(schemas []ToolSchema) []map[string]any {
	return toOpenAI(schemas, false)
}

func toOpenAI(schemas []ToolSchema, strict bool) []map[string]any {
	tools := make([]map[string]any, len(schemas))
	for i, s := range schemas {
		params := s.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		fn := map[string]any{
			"name":        SanitizeToolName(s.Name),
			"description": s.Description,
			"parameters":  params,
		}
		if strict {
			fn["parameters"] = enforceStrictSchema(params)
			fn["strict"] = true
		}
		tools[i] = map[string]any{
			"type":     "function",
			"function": fn,
		}
	}
	return tools
}

// enforceStrictSchema patches a JSON Schema object for OpenAI strict mode:
// - additionalProperties = false
// - all property names added to required
// - properties not originally required get nullable type
func enforceStrictSchema(params map[string]any) map[string]any {
	// Shallow-copy to avoid mutating the original.
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}

	props, _ := out["properties"].(map[string]any)
	if props == nil {
		out["properties"] = map[string]any{}
		out["required"] = []string{}
		out["additionalProperties"] = false
		return out
	}

	// Build set of originally required fields.
	origRequired := map[string]bool{}
	if req, ok := out["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				origRequired[s] = true
			}
		}
	}
	if req, ok := out["required"].([]string); ok {
		for _, s := range req {
			origRequired[s] = true
		}
	}

	// All properties must be in required; optional ones become nullable.
	allRequired := make([]string, 0, len(props))
	for name, propRaw := range props {
		allRequired = append(allRequired, name)
		if origRequired[name] {
			continue
		}
		// Make optional property nullable.
		prop, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := prop["type"]; ok {
			switch tv := t.(type) {
			case string:
				if tv != "null" {
					prop["type"] = []any{tv, "null"}
				}
			case []any:
				hasNull := false
				for _, v := range tv {
					if v == "null" {
						hasNull = true
						break
					}
				}
				if !hasNull {
					prop["type"] = append(tv, "null")
				}
			}
		}
	}

	out["required"] = allRequired
	out["additionalProperties"] = false
	return out
}
