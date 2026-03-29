package controlcenter

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestUploadHandler(t *testing.T) *AppUploadHandler {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apps", "test-app", "data"), 0o755)
	return &AppUploadHandler{DataDir: dir}
}

func createMultipartFile(t *testing.T, fieldName, fileName, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(content))
	writer.Close()
	return &buf, writer.FormDataContentType()
}

func TestUpload_Success(t *testing.T) {
	h := newTestUploadHandler(t)
	body, ct := createMultipartFile(t, "file", "test.txt", "hello world")

	req := httptest.NewRequest("POST", "/api/apps/test-app/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	if result["name"] != "test.txt" {
		t.Errorf("expected name=test.txt, got %v", result["name"])
	}
	if result["path"] != "uploads/test.txt" {
		t.Errorf("expected path=uploads/test.txt, got %v", result["path"])
	}

	// Verify file exists on disk
	fpath := filepath.Join(h.DataDir, "apps", "test-app", "data", "uploads", "test.txt")
	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestUpload_SanitizesFilename(t *testing.T) {
	h := newTestUploadHandler(t)
	body, ct := createMultipartFile(t, "file", "../../../etc/passwd", "evil")

	req := httptest.NewRequest("POST", "/api/apps/test-app/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	// Should sanitize to just "passwd" (filepath.Base strips ../)
	name := result["name"].(string)
	if name == "../../../etc/passwd" || name == "etc/passwd" {
		t.Errorf("filename not sanitized: %s", name)
	}
}

func TestUpload_MissingFile(t *testing.T) {
	h := newTestUploadHandler(t)
	req := httptest.NewRequest("POST", "/api/apps/test-app/upload", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUpload_InvalidSlug(t *testing.T) {
	h := newTestUploadHandler(t)
	body, ct := createMultipartFile(t, "file", "test.txt", "data")

	req := httptest.NewRequest("POST", "/api/apps/../../evil/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Should be rejected by slug validation or path mismatch
	if rec.Code == http.StatusOK {
		t.Error("expected non-200 for invalid slug")
	}
}

func TestUpload_MethodNotAllowed(t *testing.T) {
	h := newTestUploadHandler(t)
	req := httptest.NewRequest("GET", "/api/apps/test-app/upload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal.txt", "normal.txt"},
		{"../../../etc/passwd", "passwd"},
		{"file with spaces.pdf", "file_with_spaces.pdf"},
		{"", "upload"},
		{".", "upload"},
		{"..", "upload"},
		{"my-file_v2.0.tar.gz", "my-file_v2.0.tar.gz"},
	}
	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
