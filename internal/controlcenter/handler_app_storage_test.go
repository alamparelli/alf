package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestStorageHandler(t *testing.T) *AppStorageHandler {
	t.Helper()
	return &AppStorageHandler{DataDir: t.TempDir()}
}

func TestStorageGet_EmptyStore(t *testing.T) {
	h := newTestStorageHandler(t)
	req := httptest.NewRequest("GET", "/api/apps/myapp/storage", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.TrimSpace(body) != "{}" {
		t.Errorf("expected empty object, got %s", body)
	}
}

func TestStoragePut_AndGetAll(t *testing.T) {
	h := newTestStorageHandler(t)

	// PUT two keys.
	req := httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`{"color":"blue","count":42}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET all keys.
	req = httptest.NewRequest("GET", "/api/apps/myapp/storage", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET all: expected 200, got %d", rec.Code)
	}
	var store map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &store); err != nil {
		t.Fatalf("json: %v", err)
	}
	if store["color"] != "blue" {
		t.Errorf("color = %v, want blue", store["color"])
	}
	// JSON numbers decode as float64.
	if store["count"] != float64(42) {
		t.Errorf("count = %v, want 42", store["count"])
	}
}

func TestStorageGet_SingleKey(t *testing.T) {
	h := newTestStorageHandler(t)

	// Seed a key.
	req := httptest.NewRequest("PUT", "/api/apps/testapp/storage",
		strings.NewReader(`{"theme":"dark"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// GET single key.
	req = httptest.NewRequest("GET", "/api/apps/testapp/storage?key=theme", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["key"] != "theme" || resp["value"] != "dark" {
		t.Errorf("unexpected response: %s", rec.Body.String())
	}
}

func TestStorageGet_SingleKey_NotFound(t *testing.T) {
	h := newTestStorageHandler(t)
	req := httptest.NewRequest("GET", "/api/apps/myapp/storage?key=missing", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestStorageDelete_Key(t *testing.T) {
	h := newTestStorageHandler(t)

	// Seed keys.
	req := httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`{"a":"1","b":"2"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// DELETE key "a".
	req = httptest.NewRequest("DELETE", "/api/apps/myapp/storage?key=a", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE: expected 200, got %d", rec.Code)
	}

	// Verify "a" is gone but "b" remains.
	req = httptest.NewRequest("GET", "/api/apps/myapp/storage", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var store map[string]any
	json.Unmarshal(rec.Body.Bytes(), &store)
	if _, ok := store["a"]; ok {
		t.Error("key 'a' should have been deleted")
	}
	if store["b"] != "2" {
		t.Errorf("key 'b' should still exist, got %v", store["b"])
	}
}

func TestStorageDelete_MissingKeyParam(t *testing.T) {
	h := newTestStorageHandler(t)
	req := httptest.NewRequest("DELETE", "/api/apps/myapp/storage", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestStorage_InvalidAppName(t *testing.T) {
	h := newTestStorageHandler(t)
	// Slugs with dots or special chars should fail validation (400).
	// Note: "../evil" is cleaned by net/http before reaching the handler,
	// and spaces are invalid in URLs, so we test realistic invalid slugs.
	for _, slug := range []string{"a.b", "has%20space", "evil/path"} {
		req := httptest.NewRequest("GET", "/api/apps/"+slug+"/storage", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code == http.StatusOK {
			t.Errorf("slug %q: should not return 200", slug)
		}
	}
}

func TestStorage_InvalidPath(t *testing.T) {
	h := newTestStorageHandler(t)
	req := httptest.NewRequest("GET", "/api/apps/myapp/notmatch", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestStorage_MethodNotAllowed(t *testing.T) {
	h := newTestStorageHandler(t)
	req := httptest.NewRequest("POST", "/api/apps/myapp/storage", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestStoragePut_InvalidJSON(t *testing.T) {
	h := newTestStorageHandler(t)
	req := httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestStoragePut_MergesKeys(t *testing.T) {
	h := newTestStorageHandler(t)

	// First PUT.
	req := httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`{"a":"1"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Second PUT merges.
	req = httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`{"b":"2"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var store map[string]any
	json.Unmarshal(rec.Body.Bytes(), &store)
	if store["a"] != "1" || store["b"] != "2" {
		t.Errorf("merge failed: %v", store)
	}
}

func TestStoragePut_NullDeletesKey(t *testing.T) {
	h := newTestStorageHandler(t)

	// Seed.
	req := httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`{"x":"keep","y":"remove"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// PUT with null deletes "y".
	req = httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(`{"y":null}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var store map[string]any
	json.Unmarshal(rec.Body.Bytes(), &store)
	if _, ok := store["y"]; ok {
		t.Error("key 'y' should have been deleted via null")
	}
	if store["x"] != "keep" {
		t.Errorf("key 'x' should still exist, got %v", store["x"])
	}
}

func TestStoragePut_BodyLimit(t *testing.T) {
	h := newTestStorageHandler(t)

	// Build a body just over 1MB. The handler uses io.LimitReader(1<<20),
	// so a body larger than that gets truncated, producing invalid JSON.
	big := `{"k":"` + strings.Repeat("x", 1<<20) + `"}`
	req := httptest.NewRequest("PUT", "/api/apps/myapp/storage",
		strings.NewReader(big))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Truncated body => invalid JSON => 400.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized body, got %d", rec.Code)
	}
}

func TestStorage_ListKeys(t *testing.T) {
	h := newTestStorageHandler(t)

	// Seed data.
	req := httptest.NewRequest("PUT", "/api/apps/test-app/storage",
		strings.NewReader(`{"alpha":"1","beta":"2","gamma":"3"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// List keys.
	req = httptest.NewRequest("GET", "/api/apps/test-app/storage?list=keys", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result struct{ Keys []string }
	json.NewDecoder(rec.Body).Decode(&result)
	if len(result.Keys) != 3 {
		t.Errorf("expected 3 keys, got %d: %v", len(result.Keys), result.Keys)
	}
}

func TestStorage_ListEntries(t *testing.T) {
	h := newTestStorageHandler(t)

	// Seed data.
	req := httptest.NewRequest("PUT", "/api/apps/test-app/storage",
		strings.NewReader(`{"x":"hello","y":42}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// List entries.
	req = httptest.NewRequest("GET", "/api/apps/test-app/storage?list=entries", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result struct {
		Entries []map[string]any
	}
	json.NewDecoder(rec.Body).Decode(&result)
	if len(result.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result.Entries))
	}
	// Verify each entry has key and value fields.
	for _, e := range result.Entries {
		if _, ok := e["key"]; !ok {
			t.Error("entry missing 'key' field")
		}
		if _, ok := e["value"]; !ok {
			t.Error("entry missing 'value' field")
		}
	}
}

func TestStorage_ListKeysEmpty(t *testing.T) {
	h := newTestStorageHandler(t)

	req := httptest.NewRequest("GET", "/api/apps/empty-app/storage?list=keys", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result struct{ Keys []string }
	json.NewDecoder(rec.Body).Decode(&result)
	if len(result.Keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(result.Keys))
	}
}

func TestStorage_IsolationBetweenApps(t *testing.T) {
	h := newTestStorageHandler(t)

	// Write to app1.
	req := httptest.NewRequest("PUT", "/api/apps/app1/storage",
		strings.NewReader(`{"secret":"abc"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Read from app2 — should be empty.
	req = httptest.NewRequest("GET", "/api/apps/app2/storage", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Errorf("app2 should have empty storage, got %s", rec.Body.String())
	}
}
