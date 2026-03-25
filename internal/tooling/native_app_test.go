package tooling

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type mockAppService struct {
	apps    []AppInfo
	catalog []RemoteAppInfo
	catErr  error
	opErr   error
}

func (m *mockAppService) List() []AppInfo { return m.apps }

func (m *mockAppService) Catalog() ([]RemoteAppInfo, error) {
	return m.catalog, m.catErr
}

func (m *mockAppService) Install(slug string) error       { return m.opErr }
func (m *mockAppService) Update(slug string) error        { return m.opErr }
func (m *mockAppService) Enable(slug string) error        { return m.opErr }
func (m *mockAppService) Disable(slug string) error       { return m.opErr }
func (m *mockAppService) Uninstall(slug string) error     { return m.opErr }
func (m *mockAppService) Restart(slug string) error       { return m.opErr }
func (m *mockAppService) ServiceStatus() []ServiceStatusInfo { return nil }

func TestAppTool_List(t *testing.T) {
	svc := &mockAppService{
		apps: []AppInfo{
			{Name: "dashboard", DisplayName: "Dashboard", State: "enabled"},
		},
	}
	tool := AppNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "dashboard") {
		t.Fatalf("expected app in output, got: %s", out)
	}
}

func TestAppTool_ListEmpty(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No apps") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestAppTool_Catalog(t *testing.T) {
	svc := &mockAppService{
		catalog: []RemoteAppInfo{
			{Slug: "weather", Name: "Weather", Version: "1.0"},
		},
	}
	tool := AppNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"catalog"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "weather") {
		t.Fatalf("expected catalog in output, got: %s", out)
	}
}

func TestAppTool_CatalogError(t *testing.T) {
	svc := &mockAppService{catErr: fmt.Errorf("network error")}
	tool := AppNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"catalog"}`)
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("expected catalog error, got: %v", err)
	}
}

func TestAppTool_Install(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	out, err := tool.Run(context.Background(), `{"action":"install","slug":"weather"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "installed") {
		t.Fatalf("expected installed message, got: %s", out)
	}
}

func TestAppTool_InstallMissingSlug(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	_, err := tool.Run(context.Background(), `{"action":"install"}`)
	if err == nil || !strings.Contains(err.Error(), "slug is required") {
		t.Fatalf("expected slug required error, got: %v", err)
	}
}

func TestAppTool_InstallError(t *testing.T) {
	svc := &mockAppService{opErr: fmt.Errorf("not found in registry")}
	tool := AppNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"install","slug":"bad"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected install error, got: %v", err)
	}
}

func TestAppTool_Enable(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	out, err := tool.Run(context.Background(), `{"action":"enable","slug":"weather"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "enabled") {
		t.Fatalf("expected enabled message, got: %s", out)
	}
}

func TestAppTool_Disable(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	out, err := tool.Run(context.Background(), `{"action":"disable","slug":"weather"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("expected disabled message, got: %s", out)
	}
}

func TestAppTool_Uninstall(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	out, err := tool.Run(context.Background(), `{"action":"uninstall","slug":"weather"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "uninstalled") {
		t.Fatalf("expected uninstalled message, got: %s", out)
	}
}

func TestAppTool_UnknownAction(t *testing.T) {
	tool := AppNativeTool{Service: &mockAppService{}}

	_, err := tool.Run(context.Background(), `{"action":"nuke"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got: %v", err)
	}
}

func TestAppTool_Schema(t *testing.T) {
	tool := AppNativeTool{}
	s := tool.Schema()
	if s.Name != "app" {
		t.Fatalf("expected schema name 'app', got %q", s.Name)
	}
}
