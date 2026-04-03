package controlcenter

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestActionHandler(t *testing.T) (*AppActionHandler, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewFileAppStore(dir)
	return &AppActionHandler{Store: store}, dir
}

func actionRequest(t *testing.T, target, action, params, referer string) *http.Request {
	t.Helper()
	body := `{"target":"` + target + `","action":"` + action + `"`
	if params != "" {
		body += `,"params":` + params
	}
	body += `}`
	req := httptest.NewRequest(http.MethodPost, "/api/app-action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return req
}

func TestAppAction_Success(t *testing.T) {
	h, dir := newTestActionHandler(t)

	// Start a mock target server.
	var gotPath, gotBody, gotCaller string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCaller = r.Header.Get("X-Caller-App")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	manifest := `{"actions":{"add-item":{"params":["url","title"],"description":"Add item"}}}`
	appDir := filepath.Join(dir, "later")
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html></html>"), 0o644)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(appDir, "data", "port"), []byte(fmt.Sprintf("%d", port)), 0o644)

	req := actionRequest(t, "later", "add-item", `{"url":"https://example.com"}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/actions/add-item" {
		t.Errorf("expected path /api/actions/add-item, got %q", gotPath)
	}
	if gotCaller != "reader" {
		t.Errorf("expected X-Caller-App=reader, got %q", gotCaller)
	}
	if !strings.Contains(gotBody, "https://example.com") {
		t.Errorf("expected params forwarded, got %q", gotBody)
	}
}

func TestAppAction_MethodNotAllowed(t *testing.T) {
	h, _ := newTestActionHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/app-action", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestAppAction_NoReferer(t *testing.T) {
	h, _ := newTestActionHandler(t)
	req := actionRequest(t, "later", "add-item", `{}`, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestAppAction_InvalidTarget(t *testing.T) {
	h, _ := newTestActionHandler(t)
	req := actionRequest(t, "bad/slug", "add-item", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAppAction_PathTraversal(t *testing.T) {
	h, _ := newTestActionHandler(t)
	req := actionRequest(t, "later", "../../admin", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAppAction_TargetNoManifest(t *testing.T) {
	h, dir := newTestActionHandler(t)
	// Create app dir without manifest.
	appDir := filepath.Join(dir, "later")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html></html>"), 0o644)

	req := actionRequest(t, "later", "add-item", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestAppAction_ActionNotDeclared(t *testing.T) {
	h, dir := newTestActionHandler(t)
	manifest := `{"actions":{"list":{"description":"List items"}}}`
	appDir := filepath.Join(dir, "later")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)

	req := actionRequest(t, "later", "delete-all", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestAppAction_EmptyActions(t *testing.T) {
	h, dir := newTestActionHandler(t)
	appDir := filepath.Join(dir, "later")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(`{"actions":{}}`), 0o644)

	req := actionRequest(t, "later", "add-item", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestAppAction_TargetNoServer(t *testing.T) {
	h, dir := newTestActionHandler(t)
	manifest := `{"actions":{"add-item":{"description":"Add"}}}`
	appDir := filepath.Join(dir, "later")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)
	// No data/port file.

	req := actionRequest(t, "later", "add-item", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

func TestAppAction_TargetServerDown(t *testing.T) {
	h, dir := newTestActionHandler(t)
	manifest := `{"actions":{"add-item":{"description":"Add"}}}`
	appDir := filepath.Join(dir, "later")
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)
	// Port points to nothing.
	os.WriteFile(filepath.Join(appDir, "data", "port"), []byte("19999"), 0o644)

	req := actionRequest(t, "later", "add-item", `{}`, "http://localhost/apps/reader/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

func TestAppAction_SensitiveHeadersStripped(t *testing.T) {
	h, dir := newTestActionHandler(t)

	var gotCookie, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	manifest := `{"actions":{"ping":{"description":"Ping"}}}`
	appDir := filepath.Join(dir, "target")
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(appDir, "data", "port"), []byte(fmt.Sprintf("%d", port)), 0o644)

	req := actionRequest(t, "target", "ping", `{}`, "http://localhost/apps/caller/index.html")
	req.Header.Set("Cookie", "cc_session=secret")
	req.Header.Set("Authorization", "Bearer token123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotCookie != "" {
		t.Errorf("Cookie should be stripped, got %q", gotCookie)
	}
	if gotAuth != "" {
		t.Errorf("Authorization should be stripped, got %q", gotAuth)
	}
}

func TestAppAction_CallerHeaderInjected(t *testing.T) {
	h, dir := newTestActionHandler(t)

	var gotCaller string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCaller = r.Header.Get("X-Caller-App")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	manifest := `{"actions":{"ping":{"description":"Ping"}}}`
	appDir := filepath.Join(dir, "target")
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(appDir, "data", "port"), []byte(fmt.Sprintf("%d", port)), 0o644)

	req := actionRequest(t, "target", "ping", `{}`, "http://localhost/apps/my-app/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotCaller != "my-app" {
		t.Errorf("expected X-Caller-App=my-app, got %q", gotCaller)
	}
}

func TestAppAction_ParamsForwarded(t *testing.T) {
	h, dir := newTestActionHandler(t)

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	manifest := `{"actions":{"create":{"description":"Create"}}}`
	appDir := filepath.Join(dir, "target")
	os.MkdirAll(filepath.Join(appDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(appDir, "data", "port"), []byte(fmt.Sprintf("%d", port)), 0o644)

	params := `{"title":"Test","url":"https://example.com","tags":["a","b"]}`
	req := actionRequest(t, "target", "create", params, "http://localhost/apps/sender/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var sent, received map[string]any
	json.Unmarshal([]byte(params), &sent)
	json.Unmarshal([]byte(gotBody), &received)

	if received["title"] != sent["title"] || received["url"] != sent["url"] {
		t.Errorf("params not forwarded correctly: sent %s, got %s", params, gotBody)
	}
}
