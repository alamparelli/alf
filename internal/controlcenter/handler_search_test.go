package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestSearchHandler(t *testing.T) (*SearchHandler, string) {
	t.Helper()
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "apps")
	os.MkdirAll(appsDir, 0o755)
	store := NewFileAppStore(appsDir)
	return &SearchHandler{
		AppStore: store,
		DataDir:  dir,
	}, dir
}

func TestSearchHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestSearchHandler(t)
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/api/search?q=test", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rec.Code)
		}
	}
}

func TestSearchHandler_EmptyQuery(t *testing.T) {
	h, _ := newTestSearchHandler(t)
	req := httptest.NewRequest("GET", "/api/search?q=", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Apps) != 0 || len(resp.Files) != 0 || len(resp.Docs) != 0 {
		t.Errorf("expected empty results for empty query, got apps=%d files=%d docs=%d",
			len(resp.Apps), len(resp.Files), len(resp.Docs))
	}
}

func TestSearchHandler_SearchApps(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	// Create a test app
	appDir := filepath.Join(dir, "apps", "my-dashboard")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>"), 0o644)
	os.WriteFile(filepath.Join(appDir, "app.json"), []byte(`{"name":"My Dashboard","icon":"layout-dashboard","description":"A dashboard app"}`), 0o644)

	req := httptest.NewRequest("GET", "/api/search?q=dashboard&types=apps", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body == "" {
		t.Fatal("empty response body")
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Apps) == 0 {
		t.Error("expected at least one app result for 'dashboard'")
	}
}

func TestSearchHandler_SearchFiles(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	// Create test files in workspace
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes"), 0o644)
	os.WriteFile(filepath.Join(dir, "report.txt"), []byte("report"), 0o644)
	subDir := filepath.Join(dir, "projects")
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "notes-2024.md"), []byte("# More notes"), 0o644)

	req := httptest.NewRequest("GET", "/api/search?q=notes&types=files", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Files) < 2 {
		t.Errorf("expected at least 2 file results for 'notes', got %d", len(resp.Files))
	}
}

func TestSearchHandler_ExcludesProtectedDirs(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	// Create files in always-hidden directories
	for _, d := range []string{".git", ".claude", ".cache"} {
		pd := filepath.Join(dir, d)
		os.MkdirAll(pd, 0o755)
		os.WriteFile(filepath.Join(pd, "secret.txt"), []byte("secret"), 0o644)
	}
	// Create files in visible directories (config.d, logs are now searchable)
	for _, d := range []string{"config.d", "logs"} {
		pd := filepath.Join(dir, d)
		os.MkdirAll(pd, 0o755)
		os.WriteFile(filepath.Join(pd, "secret.txt"), []byte("secret"), 0o644)
	}
	// Create a normal file
	os.WriteFile(filepath.Join(dir, "secret-notes.txt"), []byte("notes"), 0o644)

	req := httptest.NewRequest("GET", "/api/search?q=secret&types=files", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	// Should find normal file + visible dir files, but not hidden dir files
	if len(resp.Files) != 3 {
		t.Errorf("expected 3 file results (1 normal + 2 visible dirs), got %d", len(resp.Files))
	}
}

func TestSearchHandler_SearchDocs(t *testing.T) {
	h, _ := newTestSearchHandler(t)

	req := httptest.NewRequest("GET", "/api/search?q=getting&types=docs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	// Docs are embedded, so this depends on actual doc content.
	// Just verify the response is valid JSON with docs array.
	if resp.Docs == nil {
		t.Error("docs should be an array, not nil")
	}
}

func TestSearchHandler_TypeFilter(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	// Create a file that matches
	os.WriteFile(filepath.Join(dir, "test-file.txt"), []byte("content"), 0o644)

	// Search only for apps — should not return files
	req := httptest.NewRequest("GET", "/api/search?q=test&types=apps", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Files) != 0 {
		t.Errorf("expected 0 file results when types=apps, got %d", len(resp.Files))
	}
}

func TestSearchHandler_CaseInsensitive(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	os.WriteFile(filepath.Join(dir, "MyReport.txt"), []byte("report"), 0o644)

	req := httptest.NewRequest("GET", "/api/search?q=myreport&types=files", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Files) == 0 {
		t.Error("expected case-insensitive match for 'myreport' -> 'MyReport.txt'")
	}
}

func TestSearchHandler_NoResults(t *testing.T) {
	h, _ := newTestSearchHandler(t)

	req := httptest.NewRequest("GET", "/api/search?q=xyznonexistent123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(resp.Apps) != 0 || len(resp.Files) != 0 {
		t.Errorf("expected no results, got apps=%d files=%d", len(resp.Apps), len(resp.Files))
	}
}

func TestSearchFiles_SymlinkDir(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	// Create a real directory outside the data dir with a file inside.
	realDir := filepath.Join(dir, "_external")
	os.MkdirAll(realDir, 0o755)
	os.WriteFile(filepath.Join(realDir, "symlinked-report.txt"), []byte("data"), 0o644)

	// Create a symlink inside the data dir pointing to the real directory.
	symlinkPath := filepath.Join(dir, "linked-project")
	if err := os.Symlink(realDir, symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/search?q=linked-project&types=files", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	// The symlinked directory should appear as a dir result.
	found := false
	for _, f := range resp.Files {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == "linked-project" {
			if isDir, ok := m["is_dir"].(bool); !ok || !isDir {
				t.Errorf("symlinked dir should have is_dir=true, got %v", m["is_dir"])
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("expected symlinked directory 'linked-project' in search results")
	}
}

func TestSearchFiles_ExcludeParam(t *testing.T) {
	h, dir := newTestSearchHandler(t)

	// Create files in a "logs" directory and a normal file.
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)
	os.WriteFile(filepath.Join(logsDir, "app.log"), []byte("log data"), 0o644)
	os.WriteFile(filepath.Join(dir, "app-notes.txt"), []byte("notes"), 0o644)

	// Search for "app" with exclude=logs.
	req := httptest.NewRequest("GET", "/api/search?q=app&types=files&exclude=logs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	// Should find app-notes.txt but NOT logs/app.log.
	for _, f := range resp.Files {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		if filepath.Dir(path) == "logs" || path == "logs" {
			t.Errorf("file from excluded 'logs' dir should not appear: %s", path)
		}
	}
	if len(resp.Files) == 0 {
		t.Error("expected at least app-notes.txt in results")
	}
}
