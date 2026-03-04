package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestPageHandler(t *testing.T) *PageHandler {
	t.Helper()
	dir := t.TempDir()
	store := NewFileResourceStoreWithLimit(dir, ".html", 5<<20)
	return &PageHandler{Store: store}
}

func TestPageHandler_ServesHTML(t *testing.T) {
	h := newTestPageHandler(t)
	h.Store.Put("dashboard", []byte("<html><body>Hello</body></html>"))

	req := httptest.NewRequest("GET", "/pages/dashboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", xfo)
	}
	if rec.Body.String() != "<html><body>Hello</body></html>" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestPageHandler_NotFound(t *testing.T) {
	h := newTestPageHandler(t)

	req := httptest.NewRequest("GET", "/pages/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestPageHandler_EmptyName(t *testing.T) {
	h := newTestPageHandler(t)

	req := httptest.NewRequest("GET", "/pages/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for empty name, got %d", rec.Code)
	}
}

func TestPageHandler_InvalidName(t *testing.T) {
	h := newTestPageHandler(t)

	for _, name := range []string{"../escape", "foo/bar", "a.b"} {
		req := httptest.NewRequest("GET", "/pages/"+name, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 for %q, got %d", name, rec.Code)
		}
	}
}

func TestPageHandler_MethodNotAllowed(t *testing.T) {
	h := newTestPageHandler(t)

	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/pages/test", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rec.Code)
		}
	}
}
