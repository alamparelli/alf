package tooling

import (
	"context"
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

func TestResolveWildcard_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	// Create CLI tool binaries: "task" and "recall".
	os.WriteFile(filepath.Join(toolsD, "task"), []byte("#!/bin/sh"), 0o755)
	os.WriteFile(filepath.Join(toolsD, "recall"), []byte("#!/bin/sh"), 0o755)

	// Register "task" as a native tool too (simulating the duplicate).
	reg := NewRegistry(dir)
	reg.RegisterNative(&fakeNativeTool{name: "task"})
	reg.RegisterNative(&fakeNativeTool{name: "search"})

	tools := ResolveWildcard(dir, reg)

	// Count occurrences of "task".
	count := 0
	for _, n := range tools {
		if n == "task" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'task' once, got %d times in %v", count, tools)
	}

	// All 3 unique tools should be present: task, recall, search.
	seen := make(map[string]bool)
	for _, n := range tools {
		seen[n] = true
	}
	for _, expected := range []string{"task", "recall", "search"} {
		if !seen[expected] {
			t.Errorf("expected tool %q in result, got %v", expected, tools)
		}
	}
}

type fakeNativeTool struct {
	name string
}

func (f *fakeNativeTool) ToolName() string                                        { return f.name }
func (f *fakeNativeTool) Schema() ToolSchema                                      { return ToolSchema{Name: f.name, Description: "fake"} }
func (f *fakeNativeTool) Run(_ context.Context, _ string) (string, error)         { return "", nil }

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
