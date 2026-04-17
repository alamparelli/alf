package controlcenter

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeveloper_Apps(t *testing.T) {
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "apps")

	// Real app dirs carry at least one marker file.
	os.MkdirAll(filepath.Join(appsDir, "my-app"), 0o755)
	os.WriteFile(filepath.Join(appsDir, "my-app", "app.json"), []byte(`{}`), 0o644)
	os.MkdirAll(filepath.Join(appsDir, "other-app"), 0o755)
	os.WriteFile(filepath.Join(appsDir, "other-app", "manifest.json"), []byte(`{}`), 0o644)

	// Orphan/leftover empty dir (e.g. from a broken uninstall) must be excluded.
	os.MkdirAll(filepath.Join(appsDir, "orphan"), 0o755)

	os.WriteFile(filepath.Join(appsDir, "not-a-dir.txt"), []byte("x"), 0o644)
	// Hidden dirs should be excluded
	os.MkdirAll(filepath.Join(appsDir, ".hidden"), 0o755)

	h := &DeveloperHandler{DataDir: dir}
	req := httptest.NewRequest("GET", "/api/developer/apps", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Apps []string `json:"apps"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Apps) != 2 {
		t.Fatalf("expected 2 apps, got %d: %v", len(resp.Apps), resp.Apps)
	}
	for _, name := range resp.Apps {
		if strings.HasPrefix(name, ".") {
			t.Errorf("hidden dir should be excluded: %s", name)
		}
		if name == "orphan" {
			t.Errorf("orphan empty dir should be excluded")
		}
	}
}

func TestDeveloper_Tools(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(toolsDir, 0o755)
	os.WriteFile(filepath.Join(toolsDir, "grep.json"), []byte(`{"name":"grep","description":"search files"}`), 0o644)
	os.WriteFile(filepath.Join(toolsDir, "invalid.json"), []byte(`not json`), 0o644)
	os.WriteFile(filepath.Join(toolsDir, "noname.json"), []byte(`{"description":"no name field"}`), 0o644)
	os.WriteFile(filepath.Join(toolsDir, "README.md"), []byte(`# ignore`), 0o644)

	h := &DeveloperHandler{DataDir: dir}
	req := httptest.NewRequest("GET", "/api/developer/tools", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Tools []map[string]any `json:"tools"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Tools) != 1 {
		t.Fatalf("expected 1 valid tool, got %d", len(resp.Tools))
	}
	if resp.Tools[0]["name"] != "grep" {
		t.Errorf("expected tool name 'grep', got %v", resp.Tools[0]["name"])
	}
}

func TestDeveloper_Skills(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	os.MkdirAll(filepath.Join(skillsDir, "my-skill"), 0o755)
	os.WriteFile(filepath.Join(skillsDir, "my-skill", "SKILL.md"), []byte("# Skill"), 0o644)
	os.MkdirAll(filepath.Join(skillsDir, "no-skillmd"), 0o755) // no SKILL.md

	h := &DeveloperHandler{DataDir: dir}
	req := httptest.NewRequest("GET", "/api/developer/skills", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp struct {
		Skills []string `json:"skills"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d: %v", len(resp.Skills), resp.Skills)
	}
	if resp.Skills[0] != "my-skill" {
		t.Errorf("expected 'my-skill', got %s", resp.Skills[0])
	}
}

func TestDeveloper_AppMeta(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "test-app")
	os.MkdirAll(filepath.Join(appDir, "bin"), 0o755)
	os.WriteFile(filepath.Join(appDir, "app.json"), []byte(`{"name":"Test App","icon":"star"}`), 0o644)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(`{"name":"Test App","version":"1.0.0","category":"games"}`), 0o644)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html></html>"), 0o644)

	h := &DeveloperHandler{DataDir: dir}
	req := httptest.NewRequest("GET", "/api/developer/app-meta?slug=test-app", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp["slug"] != "test-app" {
		t.Errorf("expected slug 'test-app', got %v", resp["slug"])
	}
	if resp["has_index"] != true {
		t.Errorf("expected has_index true")
	}
	manifest := resp["manifest"].(map[string]any)
	if manifest["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", manifest["version"])
	}
}

func TestDeveloper_AppMeta_InvalidSlug(t *testing.T) {
	h := &DeveloperHandler{DataDir: t.TempDir()}

	// Empty slug
	req := httptest.NewRequest("GET", "/api/developer/app-meta", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("empty slug: expected 400, got %d", rec.Code)
	}

	// Slug with dots (path traversal attempt)
	req = httptest.NewRequest("GET", "/api/developer/app-meta?slug=..%2Fetc", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("traversal slug: expected 400, got %d", rec.Code)
	}
}

func TestDeveloper_Validate(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "my-app")
	os.MkdirAll(filepath.Join(appDir, "bin"), 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html></html>"), 0o644)

	h := &DeveloperHandler{DataDir: dir}

	// Missing required fields
	body := `{"slug":"","name":"","version":"1.0.0"}`
	req := httptest.NewRequest("POST", "/api/developer/validate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp struct {
		Errors   []string `json:"errors"`
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Errors) < 2 {
		t.Errorf("expected at least 2 errors for empty slug+name, got %d: %v", len(resp.Errors), resp.Errors)
	}

	// Valid app with missing binary
	body = `{"slug":"my-app","name":"My App","version":"1.0.0","tools":["grep"]}`
	req = httptest.NewRequest("POST", "/api/developer/validate", strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	json.Unmarshal(rec.Body.Bytes(), &resp)
	found := false
	for _, e := range resp.Errors {
		if strings.Contains(e, "binary") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected binary error, got: %v", resp.Errors)
	}
}

func TestDeveloper_Status_NoVault(t *testing.T) {
	h := &DeveloperHandler{DataDir: t.TempDir()}
	req := httptest.NewRequest("GET", "/api/developer/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp["connected"] != false {
		t.Errorf("expected connected=false without vault")
	}
}

func TestDeveloper_NotFound(t *testing.T) {
	h := &DeveloperHandler{DataDir: t.TempDir()}
	req := httptest.NewRequest("GET", "/api/developer/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}
