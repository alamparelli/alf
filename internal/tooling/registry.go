package tooling

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/sandbox/integrity"
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
	Integrity   *integrity.IntegrityGuard // optional: skip quarantined tools from scan

	// capReg is the unified capability registry. When non-nil, every
	// RegisterNative call also registers the tool as a KindTool Capability.
	// Introduced by #338 C1 (dual-registration). Consumers migrate in C2.
	capReg *capability.Registry

	// wasmNames is the set of bundle ids registered through
	// RegisterWasmTool (#423). Tracked separately from natives so
	// ResolveWildcard can include WASM tools in `*` expansion without
	// re-categorising the schemas map.
	wasmNames map[string]bool
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

// SecurityRule is an alias for integrity.SecurityRule.
type SecurityRule = integrity.SecurityRule

// SecurityRuleset is an alias for integrity.SecurityRuleset.
type SecurityRuleset = integrity.SecurityRuleset

// SecurityWarning is an alias for integrity.SecurityWarning.
type SecurityWarning = integrity.SecurityWarning

// SecurityWarnings returns warnings from the last tool scan.
func (r *Registry) SecurityWarnings() []SecurityWarning {
	return r.secWarnings
}

// auditToolSource is a thin wrapper for integrity.AuditToolSource kept for
// the internal scan path. It will disappear once the scan path moves.
func auditToolSource(toolPath, toolName string) []SecurityWarning {
	return integrity.AuditToolSource(toolPath, toolName)
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

	// #420 — only tools.d/ (image-baked symlinks for maintainer code, TCB)
	// is scanned by the tooling registry. The legacy ~/data/tools/<name>
	// user-script path is retired per ARCHITECTURE-SECURITY.md §4.1; user
	// tools must be wasm-tool bundles in ~/data/tools/<id>/, loaded by the
	// WASM loader (internal/runtime/wasm/loader.go), not by this scanner.
	// Flat user-script files left over from the pre-lockdown layout are
	// logged once at scan time so the operator sees what to migrate.
	systemDir := filepath.Join(r.dataDir, "tools.d")
	{
		entries, err := os.ReadDir(systemDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".quarantined") {
					continue
				}
				path := filepath.Join(systemDir, e.Name())
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
	}
	// Log legacy user tools (flat files in ~/data/tools/) so the operator
	// can migrate or remove them. Subdirectories (wasm-tool bundles) are
	// ignored here — they are the WASM loader's responsibility.
	userDir := filepath.Join(r.dataDir, "tools")
	if entries, err := os.ReadDir(userDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".quarantined") {
				continue
			}
			log.Printf("tooling: ignoring legacy user-tool %s — §4.1 lockdown requires wasm-tool bundles in ~/data/tools/<id>/ (see docs/wasm-tools.md)", e.Name())
		}
	}
	if len(r.schemas) > 0 {
		names := make([]string, 0, len(r.schemas))
		for n := range r.schemas {
			names = append(names, n)
		}
		log.Printf("tooling: loaded %d tool schemas: %v", len(r.schemas), names)
	}

	// #420 — under the §4.1 lockdown user bash/Python tools in
	// ~/data/tools/<name> are refused at discovery time (the log loop
	// above flags them). No source-audit is needed because the files
	// never become invocable. The auditor is retained for the
	// post-lockdown wasm-builder path (Go sources audit) once #392
	// stage 3 wires it up.
	r.secWarnings = nil
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
// If a capability.Registry has been attached via SetCapabilityRegistry, the
// tool is also mirrored there as a KindTool Capability (dual-registration
// during #338 C1).
func (r *Registry) RegisterNative(t NativeTool) {
	r.schemas[t.ToolName()] = t.Schema()
	if r.natives == nil {
		r.natives = make(map[string]NativeTool)
	}
	r.natives[t.ToolName()] = t
	r.nativeNames = append(r.nativeNames, t.ToolName())

	if r.capReg != nil {
		if err := r.capReg.Register(asCapability(t)); err != nil {
			log.Printf("tooling: capability dual-register %q: %v", t.ToolName(), err)
		}
	}
}

// SetCapabilityRegistry attaches a unified capability.Registry. Future
// RegisterNative calls will mirror the tool into it, and every previously
// registered native tool is back-filled immediately so the two registries
// stay in sync regardless of call order.
func (r *Registry) SetCapabilityRegistry(cr *capability.Registry) {
	r.capReg = cr
	if cr == nil {
		return
	}
	for _, name := range r.nativeNames {
		t, ok := r.natives[name]
		if !ok {
			continue
		}
		if _, exists := cr.Get(capability.ID(name)); exists {
			continue
		}
		if err := cr.Register(asCapability(t)); err != nil {
			log.Printf("tooling: capability back-fill %q: %v", name, err)
		}
	}
}

// CapabilityRegistry returns the attached unified registry, or nil.
func (r *Registry) CapabilityRegistry() *capability.Registry {
	return r.capReg
}

// GetNative returns a native tool by name, or nil if not found.
func (r *Registry) GetNative(name string) NativeTool {
	return r.natives[name]
}

// NativeToolNames returns only the names of native Go tools (not user tools).
func (r *Registry) NativeToolNames() []string {
	return r.nativeNames
}

// wasmToolNames holds the ids of every wasm-tool / skill bundle the
// daemon's WASM loader registered through RegisterWasmTool. Tracked
// separately from native tools so wildcard resolvers can include them
// alongside the file-scan and native-Go entries. The slice mirrors
// the natives layout (append-only at registration time).
//
// Lookup via r.schemas[name] still works the same way — the registry
// has no kind discriminator on a fetched ToolSchema.

// RegisterWasmTool adds a WASM-backed tool's schema to the registry
// and (when a capability.Registry is attached via SetCapabilityRegistry)
// mirrors the underlying Capability there. Mirrors RegisterNative's
// dual-registration pattern so the chat engine's wildcard expansion
// and direct lookup both see the bundle.
//
// schema.Name must be the bundle id (matches the manifest's `id`
// field) — the LLM tool-loop dispatches by Name. cap is the
// wasm.Adapter (which implements capability.Capability); the
// registry holds it so wildcard tier resolvers can list it without
// the daemon's wasm.Runtime reference.
//
// Used by the WASM loader after a Tier-3-signed bundle's adapter is
// constructed. See #423 for context.
func (r *Registry) RegisterWasmTool(schema ToolSchema, cap capability.Capability) {
	r.schemas[schema.Name] = schema
	if r.wasmNames == nil {
		r.wasmNames = make(map[string]bool, 4)
	}
	r.wasmNames[schema.Name] = true

	if r.capReg != nil && cap != nil {
		if _, exists := r.capReg.Get(capability.ID(schema.Name)); !exists {
			if err := r.capReg.Register(cap); err != nil {
				log.Printf("tooling: capability dual-register wasm-tool %q: %v", schema.Name, err)
			}
		}
	}
}

// WasmToolNames returns the names of every wasm-tool / skill bundle
// registered via RegisterWasmTool. Used by ResolveWildcard so tier
// definitions with `tools = ["*"]` include WASM-backed tools.
func (r *Registry) WasmToolNames() []string {
	if len(r.wasmNames) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.wasmNames))
	for n := range r.wasmNames {
		out = append(out, n)
	}
	return out
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
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".quarantined") {
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

// ResolveWildcard expands a "*" tool wildcard into all CLI + native +
// WASM tools, deduplicated. When a tool name exists in multiple sources
// the first wins by precedence (CLI → native → wasm).
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
		// #423: WASM-backed tools (signed Tier 3 manifests with
		// [tool.schema]) join wildcard resolution alongside native +
		// CLI tools. Without this, a tier config of `tools = ["*"]`
		// would skip them.
		for _, n := range reg.WasmToolNames() {
			if !seen[n] {
				seen[n] = true
				tools = append(tools, n)
			}
		}
	}
	// Sort for deterministic ordering — important for prompt caching
	// (tool definitions are part of the cached prefix).
	sort.Strings(tools)
	return tools
}

// DiscoverToolNames returns all executable tool names found in tools.d/.
// #420 — the legacy ~/data/tools/<name> user-script discovery is dropped
// under the §4.1 lockdown. WASM-tool bundles live in ~/data/tools/<id>/
// and are loaded by the WASM loader, not this scanner.
//
// Signal tools (react, status) are excluded because they require
// ALF_SIGNAL_SOCK which is only available in CLI subprocess context.
func DiscoverToolNames(dataDir string) []string {
	seen := make(map[string]bool)
	dir := filepath.Join(dataDir, "tools.d")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".quarantined") {
			continue
		}
		if signalTools[e.Name()] {
			continue
		}
		seen[e.Name()] = true
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
