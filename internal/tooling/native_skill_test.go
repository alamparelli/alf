package tooling

import (
	"context"
	"strings"
	"testing"
)

type mockSkillService struct {
	skills []SkillInfo
	detail *SkillDetail
}

func (m *mockSkillService) All() []SkillInfo { return m.skills }

func (m *mockSkillService) Get(name string) (*SkillDetail, bool) {
	if m.detail != nil && m.detail.Name == name {
		return m.detail, true
	}
	return nil, false
}

func TestSkillTool_List(t *testing.T) {
	svc := &mockSkillService{
		skills: []SkillInfo{
			{Name: "tool-creator", Description: "Create CLI tools", Source: "system"},
			{Name: "my-skill", Description: "Custom skill", Source: "user"},
		},
	}
	tool := SkillNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "tool-creator") || !strings.Contains(out, "my-skill") {
		t.Fatalf("expected skill names in output, got: %s", out)
	}
}

func TestSkillTool_ListEmpty(t *testing.T) {
	tool := SkillNativeTool{Service: &mockSkillService{}}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No skills") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestSkillTool_Get(t *testing.T) {
	svc := &mockSkillService{
		detail: &SkillDetail{
			SkillInfo: SkillInfo{Name: "tool-creator", Description: "Create tools"},
			Content:   "# Tool Creator\n\nInstructions...",
		},
	}
	tool := SkillNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"get","name":"tool-creator"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Instructions") {
		t.Fatalf("expected content in output, got: %s", out)
	}
}

func TestSkillTool_GetNotFound(t *testing.T) {
	tool := SkillNativeTool{Service: &mockSkillService{}}

	_, err := tool.Run(context.Background(), `{"action":"get","name":"missing"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestSkillTool_GetMissingName(t *testing.T) {
	tool := SkillNativeTool{Service: &mockSkillService{}}

	_, err := tool.Run(context.Background(), `{"action":"get"}`)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name required error, got: %v", err)
	}
}

func TestSkillTool_UnknownAction(t *testing.T) {
	tool := SkillNativeTool{Service: &mockSkillService{}}

	_, err := tool.Run(context.Background(), `{"action":"execute"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got: %v", err)
	}
}

func TestSkillTool_Schema(t *testing.T) {
	tool := SkillNativeTool{}
	s := tool.Schema()
	if s.Name != "skill" {
		t.Fatalf("expected schema name 'skill', got %q", s.Name)
	}
}
