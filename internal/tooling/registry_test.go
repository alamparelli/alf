package tooling

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRegistry_LoadsManifests(t *testing.T) {
	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	schema := ToolSchema{
		Name:        "recall",
		Description: "Search memory",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
	}
	data, _ := json.Marshal(schema)
	os.WriteFile(filepath.Join(toolsD, "recall.json"), data, 0o644)

	r := NewRegistry(dir)

	got, ok := r.Get("recall")
	if !ok {
		t.Fatal("expected recall schema to be loaded")
	}
	if got.Description != "Search memory" {
		t.Errorf("unexpected description: %q", got.Description)
	}
}

func TestRegistry_ForTools_Fallback(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tools.d"), 0o755)

	r := NewRegistry(dir)

	schemas := r.ForTools([]string{"unknown_tool"})
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Name != "unknown_tool" {
		t.Errorf("expected fallback name 'unknown_tool', got %q", schemas[0].Name)
	}
	if schemas[0].Description == "" {
		t.Error("expected non-empty fallback description")
	}
}

func TestToOpenAI(t *testing.T) {
	schemas := []ToolSchema{
		{
			Name:        "recall",
			Description: "Search memory",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
	result := ToOpenAI(schemas)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0]["type"] != "function" {
		t.Errorf("expected type 'function', got %v", result[0]["type"])
	}
	fn := result[0]["function"].(map[string]any)
	if fn["name"] != "recall" {
		t.Errorf("expected name 'recall', got %v", fn["name"])
	}
}

func TestRegistry_ForTools_MixedManifestAndFallback(t *testing.T) {
	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	schema := ToolSchema{
		Name:        "remember",
		Description: "Store a memory",
		Parameters:  map[string]any{"type": "object"},
	}
	data, _ := json.Marshal(schema)
	os.WriteFile(filepath.Join(toolsD, "remember.json"), data, 0o644)

	r := NewRegistry(dir)
	schemas := r.ForTools([]string{"remember", "nonexistent"})
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}
	if schemas[0].Description != "Store a memory" {
		t.Errorf("first schema should be from manifest, got %q", schemas[0].Description)
	}
	if schemas[1].Name != "nonexistent" {
		t.Errorf("second schema should be fallback for 'nonexistent', got %q", schemas[1].Name)
	}
}
