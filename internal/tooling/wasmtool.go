package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	wasmrt "github.com/alamparelli/alf/internal/runtime/wasm"
)

// WASMTool adapts a discovered WASM capability to the NativeTool interface,
// so the existing ToolRegistry and Executor dispatch to the WASM runtime
// exactly as they do for Go-native tools. No change to callers.
//
// Input contract with the guest:
//
//   - The caller's JSON arguments are piped on the guest's stdin.
//   - The guest reads them from stdin if it wants them; otherwise it
//     ignores them and does its own thing.
//   - The guest's stdout is returned as the tool's string output.
//   - The guest's stderr is surfaced as an error only on non-zero exit.
//
// For LLM tool calls this is enough: the model gives an args object, the
// tool returns a string. Richer framing (structured outputs, streaming)
// is an upgrade path for the production ABI.
type WASMTool struct {
	runtime      *wasmrt.Runtime
	manifest     *wasmrt.Manifest
	manifestPath string
	schema       ToolSchema
}

// NewWASMTool constructs a native-tool-shaped adapter from a discovered
// capability. The caller's schemaOverride (optional) replaces the default
// schema the adapter derives from the manifest — useful when a manifest
// does not yet carry an input schema and you want the LLM to see something
// richer than "text-in, text-out".
func NewWASMTool(rt *wasmrt.Runtime, discovered wasmrt.DiscoveredCapability, schemaOverride *ToolSchema) *WASMTool {
	t := &WASMTool{
		runtime:      rt,
		manifest:     discovered.Manifest,
		manifestPath: discovered.ManifestPath,
	}
	if schemaOverride != nil {
		t.schema = *schemaOverride
	} else {
		t.schema = ToolSchema{
			Name:        discovered.Manifest.Name,
			Description: defaultDescription(discovered.Manifest),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type":        "string",
						"description": "Input passed to the WASM guest on stdin (JSON string, tool-specific).",
					},
				},
				"additionalProperties": false,
			},
		}
	}
	return t
}

// ToolName implements NativeTool. Hyphens in the manifest name are
// normalized to underscores so the tool is addressable by the forms
// providers actually emit (OpenAI-style function calling converts
// hyphens in schema names, so "wasm-demo" arrives as "wasm_demo" in
// tool_call messages). The manifest can keep human-readable hyphens.
func (t *WASMTool) ToolName() string {
	return strings.ReplaceAll(t.manifest.Name, "-", "_")
}

// Schema implements NativeTool. Schema.Name is force-aligned to
// ToolName so the LLM-emitted tool_call name resolves in the registry.
func (t *WASMTool) Schema() ToolSchema {
	s := t.schema
	s.Name = t.ToolName()
	return s
}

// Run implements NativeTool. It forwards the caller's JSON args on stdin,
// invokes the WASM guest, and returns stdout. Non-zero exit surfaces as an
// error decorated with the guest's stderr.
func (t *WASMTool) Run(ctx context.Context, argsJSON string) (string, error) {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	stdin := strings.NewReader(argsJSON)

	stdout, stderr, code, err := t.runtime.InvokeTool(ctx, t.manifestPath, stdin, nil)
	if err != nil {
		return "", fmt.Errorf("wasm tool %q: %w", t.manifest.Name, err)
	}
	if code != 0 {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", code)
		}
		return "", fmt.Errorf("wasm tool %q failed: %s", t.manifest.Name, msg)
	}

	// If the guest wrote nothing, return an empty JSON object so the LLM
	// gets a well-formed response.
	if bytes.TrimSpace(stdout) == nil {
		return "{}", nil
	}
	return string(stdout), nil
}

// defaultDescription produces a reasonable human-readable description if
// the manifest only has the terse `description` field.
func defaultDescription(m *wasmrt.Manifest) string {
	if m.Description != "" {
		return m.Description
	}
	var perms []string
	if m.Permissions.Log {
		perms = append(perms, "log")
	}
	if m.Permissions.Storage {
		perms = append(perms, "storage")
	}
	if len(m.Permissions.Vault) > 0 {
		perms = append(perms, fmt.Sprintf("vault[%s]", strings.Join(m.Permissions.Vault, ",")))
	}
	if len(m.Permissions.HTTP) > 0 {
		perms = append(perms, fmt.Sprintf("http[%s]", strings.Join(m.Permissions.HTTP, ",")))
	}
	if m.Permissions.Memory {
		perms = append(perms, "memory")
	}
	if m.Permissions.Events {
		perms = append(perms, "events")
	}
	base := fmt.Sprintf("WASM-sandboxed tool %q (version %s).", m.Name, m.Version)
	if len(perms) > 0 {
		base += fmt.Sprintf(" Permissions: %s.", strings.Join(perms, ", "))
	}
	return base
}

// InputArgs is the default input struct: ToolAction uses argsJSON → guest.
// Exposed for tests.
type InputArgs struct {
	Input string `json:"input"`
}

// ExtractInput returns the plain-text `input` field from a JSON args
// string, or the raw string if it was not an object. Used when a tool
// wants the "input" shortcut without parsing.
func ExtractInput(argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	var a InputArgs
	if err := json.Unmarshal([]byte(argsJSON), &a); err == nil && a.Input != "" {
		return a.Input
	}
	return argsJSON
}
