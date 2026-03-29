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

func newTestErrorHandler(t *testing.T) *AppErrorHandler {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apps", "test-app", "data"), 0o755)
	return &AppErrorHandler{DataDir: dir}
}

func TestErrors_PostAndGet(t *testing.T) {
	h := newTestErrorHandler(t)

	// Post an error
	req := httptest.NewRequest("POST", "/api/apps/test-app/errors",
		strings.NewReader(`{"message":"TypeError: x is not a function","stack":"at foo.js:10","source":"onerror"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Get errors
	req = httptest.NewRequest("GET", "/api/apps/test-app/errors", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", rec.Code)
	}

	var result struct {
		Errors []AppErrorEntry `json:"errors"`
		Count  int             `json:"count"`
	}
	json.NewDecoder(rec.Body).Decode(&result)

	if result.Count != 1 {
		t.Errorf("expected 1 error, got %d", result.Count)
	}
	if result.Errors[0].Message != "TypeError: x is not a function" {
		t.Errorf("unexpected message: %s", result.Errors[0].Message)
	}
	if result.Errors[0].Timestamp == "" {
		t.Error("timestamp should be set by server")
	}
}

func TestErrors_RingBuffer(t *testing.T) {
	h := newTestErrorHandler(t)

	// Post 105 errors
	for i := 0; i < 105; i++ {
		req := httptest.NewRequest("POST", "/api/apps/test-app/errors",
			strings.NewReader(`{"message":"error"}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST #%d failed: %d", i, rec.Code)
		}
	}

	// Should have only 100 (maxErrorLogEntries)
	req := httptest.NewRequest("GET", "/api/apps/test-app/errors", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var result struct {
		Count int `json:"count"`
	}
	json.NewDecoder(rec.Body).Decode(&result)

	if result.Count != 100 {
		t.Errorf("expected 100 errors (ring buffer), got %d", result.Count)
	}
}

func TestErrors_EmptyMessage(t *testing.T) {
	h := newTestErrorHandler(t)

	req := httptest.NewRequest("POST", "/api/apps/test-app/errors",
		strings.NewReader(`{"message":""}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty message, got %d", rec.Code)
	}
}

func TestErrors_Clear(t *testing.T) {
	h := newTestErrorHandler(t)

	// Post an error
	req := httptest.NewRequest("POST", "/api/apps/test-app/errors",
		strings.NewReader(`{"message":"oops"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Clear
	req = httptest.NewRequest("DELETE", "/api/apps/test-app/errors", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE expected 200, got %d", rec.Code)
	}

	// Verify empty
	req = httptest.NewRequest("GET", "/api/apps/test-app/errors", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var result struct {
		Count int `json:"count"`
	}
	json.NewDecoder(rec.Body).Decode(&result)
	if result.Count != 0 {
		t.Errorf("expected 0 after clear, got %d", result.Count)
	}
}

func TestErrors_GetEmpty(t *testing.T) {
	h := newTestErrorHandler(t)

	req := httptest.NewRequest("GET", "/api/apps/fresh-app/errors", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result struct {
		Count int `json:"count"`
	}
	json.NewDecoder(rec.Body).Decode(&result)
	if result.Count != 0 {
		t.Errorf("expected 0 for fresh app, got %d", result.Count)
	}
}
