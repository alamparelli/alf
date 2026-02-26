package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestResourceHandler(t *testing.T) (*ResourceHandler, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewFileResourceStore(dir, ".md")
	return &ResourceHandler{Store: store}, dir
}

func TestResourceHandler_ListEmpty(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	req := httptest.NewRequest("GET", "/api/memories/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("expected empty items, got %s", rec.Body.String())
	}
}

func TestResourceHandler_PutAndGet(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	// PUT
	payload := `{"content":"# Test\nHello"}`
	req := httptest.NewRequest("PUT", "/api/memories/test-note", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET
	req = httptest.NewRequest("GET", "/api/memories/test-note", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"test-note"`) {
		t.Errorf("expected name in response, got %s", body)
	}
	if !strings.Contains(body, "# Test") {
		t.Errorf("expected content in response, got %s", body)
	}
}

func TestResourceHandler_Delete(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	// Create then delete
	h.Store.Put("to-delete", []byte("data"))

	req := httptest.NewRequest("DELETE", "/api/memories/to-delete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("DELETE expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify gone
	req = httptest.NewRequest("GET", "/api/memories/to-delete", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec.Code)
	}
}

func TestResourceHandler_DeleteNotFound(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	req := httptest.NewRequest("DELETE", "/api/memories/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestResourceHandler_InvalidName(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	req := httptest.NewRequest("PUT", "/api/memories/bad..name", strings.NewReader(`{"content":"x"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestResourceHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	req := httptest.NewRequest("PATCH", "/api/memories/test", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestResourceHandler_PutNoName(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	req := httptest.NewRequest("PUT", "/api/memories/", strings.NewReader(`{"content":"x"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestResourceHandler_Notifies(t *testing.T) {
	dir := t.TempDir()
	store := NewFileResourceStore(dir, ".json")
	notifier := &mockNotifier{}
	h := &ResourceHandler{Store: store, Notifier: notifier, Event: ReloadTools}

	// PUT triggers notification
	req := httptest.NewRequest("PUT", "/api/tools/my-tool", strings.NewReader(`{"content":"{}"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if len(notifier.events) != 1 || notifier.events[0] != ReloadTools {
		t.Errorf("expected ReloadTools notification, got %v", notifier.events)
	}

	// DELETE triggers notification
	req = httptest.NewRequest("DELETE", "/api/tools/my-tool", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if len(notifier.events) != 2 || notifier.events[1] != ReloadTools {
		t.Errorf("expected second ReloadTools notification, got %v", notifier.events)
	}
}

func TestResourceHandler_ListAfterPut(t *testing.T) {
	h, _ := newTestResourceHandler(t)

	h.Store.Put("alpha", []byte("a"))
	h.Store.Put("beta", []byte("b"))

	req := httptest.NewRequest("GET", "/api/memories/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Errorf("expected both items in list, got %s", body)
	}
}
