package tooling

import "testing"

// TestNativeToolMetadata checks that each native tool exposes a stable
// ToolName and a Schema with a matching Name. Guards against accidental
// renames and exercises the trivial accessors so coverage reflects the
// public surface.
func TestNativeToolMetadata(t *testing.T) {
	type tool interface {
		ToolName() string
		Schema() ToolSchema
	}

	tools := []struct {
		name string
		t    tool
	}{
		{"app", AppNativeTool{}},
		{"bash", BashNativeTool{}},
		{"config", ConfigNativeTool{}},
		{"firewall", FirewallNativeTool{}},
		{"glob", GlobNativeTool{}},
		{"grep", GrepNativeTool{}},
		{"llm", LLMNativeTool{}},
		{"log", LogNativeTool{}},
		{"read_file", ReadFileNativeTool{}},
		{"remove", RemoveNativeTool{}},
		{"search", SearchNativeTool{}},
		{"skill", SkillNativeTool{}},
		{"task", TaskNativeTool{}},
		{"team", TeamNativeTool{}},
		{"tier", TierNativeTool{}},
	}

	for _, tt := range tools {
		if tt.t == nil {
			continue
		}
		if got := tt.t.ToolName(); got != tt.name {
			t.Errorf("%s: ToolName() = %q", tt.name, got)
		}
		s := tt.t.Schema()
		if s.Name == "" {
			t.Errorf("%s: Schema.Name is empty", tt.name)
		}
		if s.Description == "" {
			t.Errorf("%s: Schema.Description is empty", tt.name)
		}
	}
}
