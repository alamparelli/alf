package tooling

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// --- Config mock & tests ---

type mockConfigService struct {
	cfg    map[string]any
	cfgErr error
}

func (m *mockConfigService) Get() (map[string]any, error) { return m.cfg, m.cfgErr }

func TestConfigTool_Get(t *testing.T) {
	svc := &mockConfigService{cfg: map[string]any{"log_level": "info", "timezone": "UTC"}}
	tool := ConfigNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"get"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "log_level") {
		t.Fatalf("expected config in output, got: %s", out)
	}
}

func TestConfigTool_GetError(t *testing.T) {
	svc := &mockConfigService{cfgErr: fmt.Errorf("file not found")}
	tool := ConfigNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"get"}`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConfigTool_UnknownAction(t *testing.T) {
	tool := ConfigNativeTool{Service: &mockConfigService{}}

	_, err := tool.Run(context.Background(), `{"action":"set"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got: %v", err)
	}
}

func TestConfigTool_Schema(t *testing.T) {
	s := ConfigNativeTool{}.Schema()
	if s.Name != "config" {
		t.Fatalf("expected 'config', got %q", s.Name)
	}
}

// --- Tier mock & tests ---

type mockTierService struct {
	tiers []TierInfo
}

func (m *mockTierService) List() []TierInfo { return m.tiers }

func TestTierTool_List(t *testing.T) {
	svc := &mockTierService{
		tiers: []TierInfo{
			{Name: "haiku", Model: "claude-haiku", Enabled: true},
			{Name: "opus", Model: "claude-opus", Enabled: true},
		},
	}
	tool := TierNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "haiku") || !strings.Contains(out, "opus") {
		t.Fatalf("expected tiers in output, got: %s", out)
	}
}

func TestTierTool_ListEmpty(t *testing.T) {
	tool := TierNativeTool{Service: &mockTierService{}}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No tiers") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestTierTool_Schema(t *testing.T) {
	s := TierNativeTool{}.Schema()
	if s.Name != "tier" {
		t.Fatalf("expected 'tier', got %q", s.Name)
	}
}

// --- Log mock & tests ---

type mockLogService struct {
	available []string
	lines     []string
	tailErr   error
}

func (m *mockLogService) Available() []string                    { return m.available }
func (m *mockLogService) Tail(name string, lines int) ([]string, error) { return m.lines, m.tailErr }

func TestLogTool_List(t *testing.T) {
	svc := &mockLogService{available: []string{"daemon.log", "chat.log"}}
	tool := LogNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "daemon.log") {
		t.Fatalf("expected log names in output, got: %s", out)
	}
}

func TestLogTool_ListEmpty(t *testing.T) {
	tool := LogNativeTool{Service: &mockLogService{}}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No log") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestLogTool_Tail(t *testing.T) {
	svc := &mockLogService{lines: []string{"line1", "line2", "line3"}}
	tool := LogNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"tail","name":"daemon.log","lines":50}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "line2") {
		t.Fatalf("expected log lines in output, got: %s", out)
	}
}

func TestLogTool_TailDefaultLines(t *testing.T) {
	svc := &mockLogService{lines: []string{"entry"}}
	tool := LogNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"tail","name":"daemon.log"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "entry") {
		t.Fatalf("expected log content, got: %s", out)
	}
}

func TestLogTool_TailMissingName(t *testing.T) {
	tool := LogNativeTool{Service: &mockLogService{}}

	_, err := tool.Run(context.Background(), `{"action":"tail"}`)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name required error, got: %v", err)
	}
}

func TestLogTool_TailError(t *testing.T) {
	svc := &mockLogService{tailErr: fmt.Errorf("log not found")}
	tool := LogNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"tail","name":"bad.log"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected error, got: %v", err)
	}
}

func TestLogTool_Schema(t *testing.T) {
	s := LogNativeTool{}.Schema()
	if s.Name != "log" {
		t.Fatalf("expected 'log', got %q", s.Name)
	}
}

// --- Search mock & tests ---

type mockSearchService struct {
	results []SearchResult
	err     error
}

func (m *mockSearchService) Search(query string, types []string) ([]SearchResult, error) {
	return m.results, m.err
}

func TestSearchTool_Search(t *testing.T) {
	svc := &mockSearchService{
		results: []SearchResult{
			{Type: "app", Name: "weather", Desc: "Weather app"},
			{Type: "file", Name: "notes.md", Path: "/data/notes.md"},
		},
	}
	tool := SearchNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"query":"weather"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "weather") {
		t.Fatalf("expected results in output, got: %s", out)
	}
}

func TestSearchTool_SearchWithTypes(t *testing.T) {
	svc := &mockSearchService{
		results: []SearchResult{{Type: "file", Name: "test.txt"}},
	}
	tool := SearchNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"query":"test","types":"files"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "test.txt") {
		t.Fatalf("expected file result, got: %s", out)
	}
}

func TestSearchTool_SearchEmpty(t *testing.T) {
	tool := SearchNativeTool{Service: &mockSearchService{}}

	out, err := tool.Run(context.Background(), `{"query":"nothing"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No results") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestSearchTool_SearchMissingQuery(t *testing.T) {
	tool := SearchNativeTool{Service: &mockSearchService{}}

	_, err := tool.Run(context.Background(), `{"query":""}`)
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("expected query required error, got: %v", err)
	}
}

func TestSearchTool_SearchError(t *testing.T) {
	svc := &mockSearchService{err: fmt.Errorf("index unavailable")}
	tool := SearchNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"query":"test"}`)
	if err == nil || !strings.Contains(err.Error(), "index unavailable") {
		t.Fatalf("expected error, got: %v", err)
	}
}

func TestSearchTool_Schema(t *testing.T) {
	s := SearchNativeTool{}.Schema()
	if s.Name != "search" {
		t.Fatalf("expected 'search', got %q", s.Name)
	}
}
