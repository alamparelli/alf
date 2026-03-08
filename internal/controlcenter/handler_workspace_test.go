package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestWorkspaceHandler(t *testing.T) (*WorkspaceHandler, string, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	skillsDir := t.TempDir()

	// Create symlinks inside dataDir like the daemon does.
	os.Symlink(configDir, filepath.Join(dataDir, "config.d"))
	os.Symlink(skillsDir, filepath.Join(dataDir, "skills.d"))

	// Create tools.d as a real directory (like linkSystemTools does).
	os.MkdirAll(filepath.Join(dataDir, "tools.d"), 0o755)

	h := &WorkspaceHandler{
		DataDir:   dataDir,
		ConfigDir: configDir,
		SkillsDir: skillsDir,
	}
	return h, dataDir, configDir, skillsDir
}

func wsGet(h *WorkspaceHandler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/workspace?path="+path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func wsPut(h *WorkspaceHandler, path, content string) *httptest.ResponseRecorder {
	body := `{"content":"` + content + `"}`
	req := httptest.NewRequest("PUT", "/api/workspace?path="+path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func wsDel(h *WorkspaceHandler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("DELETE", "/api/workspace?path="+path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- Symlinked directory browsing ---

func TestWorkspace_ConfigDSymlinkListable(t *testing.T) {
	h, _, configDir, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{}`), 0o644)

	rec := wsGet(h, "config.d")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Type    string    `json:"type"`
		Entries []wsEntry `json:"entries"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Type != "directory" {
		t.Fatalf("expected directory, got %q", resp.Type)
	}
	found := false
	for _, e := range resp.Entries {
		if e.Name == "config.json" {
			found = true
		}
	}
	if !found {
		t.Error("config.json not found in config.d listing")
	}
}

func TestWorkspace_SkillsDSymlinkListable(t *testing.T) {
	h, _, _, skillsDir := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(skillsDir, "my-skill.md"), []byte("# Skill"), 0o644)

	rec := wsGet(h, "skills.d")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Type    string    `json:"type"`
		Entries []wsEntry `json:"entries"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Type != "directory" {
		t.Fatalf("expected directory, got %q", resp.Type)
	}
	found := false
	for _, e := range resp.Entries {
		if e.Name == "my-skill.md" {
			found = true
		}
	}
	if !found {
		t.Error("my-skill.md not found in skills.d listing")
	}
}

func TestWorkspace_ConfigDFileReadable(t *testing.T) {
	h, _, configDir, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(configDir, "tiers.json"), []byte(`{"tiers":[]}`), 0o644)

	rec := wsGet(h, "config.d/tiers.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Type     string `json:"type"`
		Content  string `json:"content"`
		Editable bool   `json:"editable"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Type != "file" {
		t.Fatalf("expected file, got %q", resp.Type)
	}
	if resp.Content != `{"tiers":[]}` {
		t.Errorf("unexpected content: %q", resp.Content)
	}
}

// --- Read-only enforcement ---

func TestWorkspace_ConfigDReadOnly_PUT(t *testing.T) {
	h, _, _, _ := newTestWorkspaceHandler(t)

	rec := wsPut(h, "config.d/config.json", `{"key":"val"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Error("expected read-only error message")
	}
}

func TestWorkspace_ConfigDReadOnly_DELETE(t *testing.T) {
	h, _, configDir, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(configDir, "test.json"), []byte(`{}`), 0o644)

	rec := wsDel(h, "config.d/test.json")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspace_SkillsDReadOnly_PUT(t *testing.T) {
	h, _, _, _ := newTestWorkspaceHandler(t)

	rec := wsPut(h, "skills.d/something.md", "content")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspace_SkillsDReadOnly_DELETE(t *testing.T) {
	h, _, _, skillsDir := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(skillsDir, "skill.md"), []byte("x"), 0o644)

	rec := wsDel(h, "skills.d/skill.md")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspace_ToolsDReadOnly_PUT(t *testing.T) {
	h, _, _, _ := newTestWorkspaceHandler(t)

	rec := wsPut(h, "tools.d/hack.sh", "#!/bin/bash")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspace_ToolsDReadOnly_DELETE(t *testing.T) {
	h, dataDir, _, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(dataDir, "tools.d", "tool.sh"), []byte("x"), 0o644)

	rec := wsDel(h, "tools.d/tool.sh")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspace_ConfigDFileNotEditable(t *testing.T) {
	h, _, configDir, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(configDir, "tiers.json"), []byte(`{}`), 0o644)

	rec := wsGet(h, "config.d/tiers.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Editable bool `json:"editable"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Editable {
		t.Error("config.d files should not be editable")
	}
}

// --- Normal files remain writable ---

func TestWorkspace_NormalFilePUT(t *testing.T) {
	h, dataDir, _, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(dataDir, "notes.md"), []byte("old"), 0o644)

	rec := wsPut(h, "notes.md", "new content")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	data, _ := os.ReadFile(filepath.Join(dataDir, "notes.md"))
	if string(data) != "new content" {
		t.Errorf("file not updated: %q", string(data))
	}
}

func TestWorkspace_NormalFileEditable(t *testing.T) {
	h, dataDir, _, _ := newTestWorkspaceHandler(t)
	os.WriteFile(filepath.Join(dataDir, "notes.md"), []byte("hello"), 0o644)

	rec := wsGet(h, "notes.md")
	var resp struct {
		Editable bool `json:"editable"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Editable {
		t.Error("normal .md files should be editable")
	}
}

// --- Path traversal blocked ---

func TestWorkspace_PathTraversalBlocked(t *testing.T) {
	h, _, _, _ := newTestWorkspaceHandler(t)

	rec := wsGet(h, "../../../etc/passwd")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Root listing includes readOnly metadata ---

func TestWorkspace_RootListingIncludesReadOnly(t *testing.T) {
	h, _, _, _ := newTestWorkspaceHandler(t)

	rec := wsGet(h, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		ReadOnly []string `json:"readOnly"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.ReadOnly) == 0 {
		t.Fatal("expected readOnly list in root listing")
	}

	roSet := map[string]bool{}
	for _, r := range resp.ReadOnly {
		roSet[r] = true
	}
	for _, expected := range []string{"config.d", "skills.d", "tools.d"} {
		if !roSet[expected] {
			t.Errorf("%q missing from readOnly list", expected)
		}
	}
}
