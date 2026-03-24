package tooling

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type mockTeamService struct {
	teams   []TeamInfo
	saveErr error
	delErr  error
}

func (m *mockTeamService) All() []TeamInfo { return m.teams }

func (m *mockTeamService) Get(name string) (*TeamInfo, bool) {
	for _, t := range m.teams {
		if t.Name == name {
			return &t, true
		}
	}
	return nil, false
}

func (m *mockTeamService) Save(req TeamSaveRequest) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	return nil
}

func (m *mockTeamService) Delete(nameOrID string) error {
	if m.delErr != nil {
		return m.delErr
	}
	for _, t := range m.teams {
		if t.Name == nameOrID || t.ID == nameOrID {
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func TestTeamTool_List(t *testing.T) {
	svc := &mockTeamService{
		teams: []TeamInfo{
			{ID: "1", Name: "ops", Agents: []AgentInfo{{Name: "planner", Tier: "sonnet"}}},
		},
	}
	tool := TeamNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ops") {
		t.Fatalf("expected team name in output, got: %s", out)
	}
}

func TestTeamTool_ListEmpty(t *testing.T) {
	tool := TeamNativeTool{Service: &mockTeamService{}}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No teams") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestTeamTool_Get(t *testing.T) {
	svc := &mockTeamService{
		teams: []TeamInfo{
			{ID: "1", Name: "ops", Description: "Operations team", Agents: []AgentInfo{{Name: "runner", Tier: "haiku"}}},
		},
	}
	tool := TeamNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"get","name":"ops"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Operations team") {
		t.Fatalf("expected description in output, got: %s", out)
	}
}

func TestTeamTool_GetNotFound(t *testing.T) {
	tool := TeamNativeTool{Service: &mockTeamService{}}

	_, err := tool.Run(context.Background(), `{"action":"get","name":"missing"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestTeamTool_GetMissingName(t *testing.T) {
	tool := TeamNativeTool{Service: &mockTeamService{}}

	_, err := tool.Run(context.Background(), `{"action":"get"}`)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name required error, got: %v", err)
	}
}

func TestTeamTool_Save(t *testing.T) {
	svc := &mockTeamService{}
	tool := TeamNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"save","name":"dev","description":"Dev team","agents":[{"name":"coder","tier":"opus"},{"name":"reviewer","tier":"sonnet"}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "saved") {
		t.Fatalf("expected saved message, got: %s", out)
	}
}

func TestTeamTool_SaveNoAgents(t *testing.T) {
	tool := TeamNativeTool{Service: &mockTeamService{}}

	_, err := tool.Run(context.Background(), `{"action":"save","name":"empty"}`)
	if err == nil || !strings.Contains(err.Error(), "at least one agent") {
		t.Fatalf("expected agent required error, got: %v", err)
	}
}

func TestTeamTool_SaveError(t *testing.T) {
	svc := &mockTeamService{saveErr: fmt.Errorf("disk full")}
	tool := TeamNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"save","name":"t","agents":[{"name":"a","tier":"haiku"}]}`)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected save error, got: %v", err)
	}
}

func TestTeamTool_Delete(t *testing.T) {
	svc := &mockTeamService{
		teams: []TeamInfo{{ID: "1", Name: "old"}},
	}
	tool := TeamNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"delete","name":"old"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "deleted") {
		t.Fatalf("expected deleted message, got: %s", out)
	}
}

func TestTeamTool_DeleteError(t *testing.T) {
	svc := &mockTeamService{delErr: fmt.Errorf("permission denied")}
	tool := TeamNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"delete","name":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected delete error, got: %v", err)
	}
}

func TestTeamTool_UnknownAction(t *testing.T) {
	tool := TeamNativeTool{Service: &mockTeamService{}}

	_, err := tool.Run(context.Background(), `{"action":"fly"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got: %v", err)
	}
}

func TestTeamTool_Schema(t *testing.T) {
	tool := TeamNativeTool{}
	s := tool.Schema()
	if s.Name != "team" {
		t.Fatalf("expected schema name 'team', got %q", s.Name)
	}
}
