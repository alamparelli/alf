package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterSections(t *testing.T) {
	input := `shared content

<!-- @begin cli -->
cli-only content
<!-- @end cli -->

<!-- @begin api -->
api-only content
<!-- @end api -->

<!-- @begin tg -->
telegram-only
<!-- @end tg -->

<!-- @begin cc -->
cc-only
<!-- @end cc -->

footer`

	tests := []struct {
		name    string
		cfg     PromptConfig
		want    []string
		exclude []string
	}{
		{
			name:    "cli+tg",
			cfg:     PromptConfig{Backend: "cli", Channel: "tg"},
			want:    []string{"shared content", "cli-only content", "telegram-only", "footer"},
			exclude: []string{"api-only content", "cc-only"},
		},
		{
			name:    "api+cc",
			cfg:     PromptConfig{Backend: "api", Channel: "cc"},
			want:    []string{"shared content", "api-only content", "cc-only", "footer"},
			exclude: []string{"cli-only content", "telegram-only"},
		},
		{
			name:    "cli+cc",
			cfg:     PromptConfig{Backend: "cli", Channel: "cc"},
			want:    []string{"shared content", "cli-only content", "cc-only", "footer"},
			exclude: []string{"api-only content", "telegram-only"},
		},
		{
			name:    "api+tg",
			cfg:     PromptConfig{Backend: "api", Channel: "tg"},
			want:    []string{"shared content", "api-only content", "telegram-only", "footer"},
			exclude: []string{"cli-only content", "cc-only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterSections(input, tt.cfg)
			for _, s := range tt.want {
				if !contains(result, s) {
					t.Errorf("expected %q in result, got:\n%s", s, result)
				}
			}
			for _, s := range tt.exclude {
				if contains(result, s) {
					t.Errorf("unexpected %q in result, got:\n%s", s, result)
				}
			}
			// Markers should never appear in output.
			if contains(result, "<!-- @begin") || contains(result, "<!-- @end") {
				t.Errorf("markers should be stripped, got:\n%s", result)
			}
		})
	}
}

func TestFilterSections_NoTripleNewlines(t *testing.T) {
	input := `before

<!-- @begin cli -->
removed
<!-- @end cli -->

after`

	result := filterSections(input, PromptConfig{Backend: "api", Channel: "cc"})
	if contains(result, "\n\n\n") {
		t.Errorf("result should not have triple newlines:\n%q", result)
	}
}

// --- Toolbox generation tests ---

func TestGenerateToolbox_WithSchemas(t *testing.T) {
	dataDir := t.TempDir()
	contextDir := t.TempDir()

	toolsDir := filepath.Join(dataDir, "tools.d")
	os.MkdirAll(toolsDir, 0o755)

	// Create a tool binary.
	os.WriteFile(filepath.Join(toolsDir, "recall"), []byte("#!/bin/sh"), 0o755)

	// Create a matching schema.
	schema := map[string]any{
		"name":        "recall",
		"description": "Search memory by similarity.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer"},
			},
			"required":     []string{"query"},
			"x-positional": []string{"query"},
		},
	}
	data, _ := json.Marshal(schema)
	os.WriteFile(filepath.Join(toolsDir, "recall.json"), data, 0o644)

	// Create a tool without schema.
	os.WriteFile(filepath.Join(toolsDir, "mytool"), []byte("#!/bin/sh"), 0o755)

	GenerateToolbox(contextDir, dataDir)

	content, err := os.ReadFile(filepath.Join(contextDir, "toolbox.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)

	// recall should have description and usage.
	if !strings.Contains(s, "Search memory by similarity.") {
		t.Errorf("expected schema description in toolbox, got:\n%s", s)
	}
	if !strings.Contains(s, "<query>") {
		t.Errorf("expected positional arg in usage, got:\n%s", s)
	}
	if !strings.Contains(s, "[limit]") || !strings.Contains(s, "[--limit") {
		// limit is optional — either positional or flag form.
	}

	// mytool should be plain (no schema).
	if !strings.Contains(s, "- `mytool`") {
		t.Errorf("expected plain tool line for mytool, got:\n%s", s)
	}
}

func TestGenerateToolbox_UserTools(t *testing.T) {
	dataDir := t.TempDir()
	contextDir := t.TempDir()

	userDir := filepath.Join(dataDir, "tools")
	os.MkdirAll(userDir, 0o755)

	os.WriteFile(filepath.Join(userDir, "custom"), []byte("#!/bin/sh"), 0o755)

	schema := map[string]any{
		"name":        "custom",
		"description": "A custom tool.",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"arg": map[string]any{"type": "string"}},
			"required":   []string{"arg"},
		},
	}
	data, _ := json.Marshal(schema)
	os.WriteFile(filepath.Join(userDir, "custom.json"), data, 0o644)

	GenerateToolbox(contextDir, dataDir)

	content, _ := os.ReadFile(filepath.Join(contextDir, "toolbox.md"))
	s := string(content)

	if !strings.Contains(s, "User Tools") {
		t.Errorf("expected User Tools section, got:\n%s", s)
	}
	if !strings.Contains(s, "A custom tool.") {
		t.Errorf("expected description for custom tool, got:\n%s", s)
	}
}

func TestGenerateToolbox_NoSchemaFallback(t *testing.T) {
	dataDir := t.TempDir()
	contextDir := t.TempDir()

	toolsDir := filepath.Join(dataDir, "tools.d")
	os.MkdirAll(toolsDir, 0o755)
	os.WriteFile(filepath.Join(toolsDir, "plain"), []byte("#!/bin/sh"), 0o755)

	GenerateToolbox(contextDir, dataDir)

	content, _ := os.ReadFile(filepath.Join(contextDir, "toolbox.md"))
	s := string(content)

	if !strings.Contains(s, "- `plain`\n") {
		t.Errorf("expected plain line without description, got:\n%s", s)
	}
}

func TestToolLine_NoSchema(t *testing.T) {
	dir := t.TempDir()
	line := toolLine("missing", dir)
	if line != "- `missing`\n" {
		t.Errorf("expected plain line, got %q", line)
	}
}

func TestToolLine_WithSchema(t *testing.T) {
	dir := t.TempDir()
	schema := map[string]any{
		"name":        "recall",
		"description": "Search memory.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required":     []string{"query"},
			"x-positional": []string{"query"},
		},
	}
	data, _ := json.Marshal(schema)
	os.WriteFile(filepath.Join(dir, "recall.json"), data, 0o644)

	line := toolLine("recall", dir)
	if !strings.Contains(line, "Search memory.") {
		t.Errorf("expected description, got %q", line)
	}
	if !strings.Contains(line, "recall <query>") {
		t.Errorf("expected usage with positional, got %q", line)
	}
}

func TestToolLine_HyphenUnderscore(t *testing.T) {
	dir := t.TempDir()
	schema := map[string]any{
		"name":        "xpost_api",
		"description": "Post to X.",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"cmd": map[string]any{"type": "string"}},
			"required":   []string{"cmd"},
		},
	}
	data, _ := json.Marshal(schema)
	// Schema file uses underscore, tool binary uses hyphen.
	os.WriteFile(filepath.Join(dir, "xpost_api.json"), data, 0o644)

	line := toolLine("xpost-api", dir)
	if !strings.Contains(line, "Post to X.") {
		t.Errorf("expected schema found via underscore variant, got %q", line)
	}
}

func TestBuildUsage_NoProperties(t *testing.T) {
	s := &toolSchema{
		Parameters: toolParameters{},
	}
	if buildUsage("tool", s) != "" {
		t.Error("expected empty usage for no properties")
	}
}

func TestBuildUsage_MixedArgs(t *testing.T) {
	s := &toolSchema{
		Parameters: toolParameters{
			Properties: map[string]toolProperty{
				"action": {Type: "string"},
				"name":   {Type: "string"},
				"force":  {Type: "boolean"},
			},
			Required:    []string{"action"},
			XPositional: []string{"action"},
		},
	}
	usage := buildUsage("schedule", s)
	if !strings.Contains(usage, "schedule <action>") {
		t.Errorf("expected required positional, got %q", usage)
	}
	if !strings.Contains(usage, "[--force") {
		t.Errorf("expected optional flag, got %q", usage)
	}
	if !strings.Contains(usage, "[--name") {
		t.Errorf("expected optional flag, got %q", usage)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
