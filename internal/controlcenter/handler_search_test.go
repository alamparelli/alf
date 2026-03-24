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

	// Create files in protected directories
	protectedDirs := []string{"config.d", "logs", "sessions", ".git"}
	for _, d := range protectedDirs {
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
	// Should only find the normal file, not files in protected dirs
	if len(resp.Files) != 1 {
		t.Errorf("expected exactly 1 file result (excluding protected dirs), got %d", len(resp.Files))
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
