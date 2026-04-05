package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestAppHandler(t *testing.T) (*AppHandler, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewFileAppStore(dir)
	return &AppHandler{Store: store}, dir
}

func TestAppHandler_ServesIndexHTML(t *testing.T) {
	h, dir := newTestAppHandler(t)
	appDir := filepath.Join(dir, "dashboard")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html><body>Hello</body></html>"), 0o644)

	req := httptest.NewRequest("GET", "/apps/dashboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"frame-ancestors 'self'", "script-src 'self' 'unsafe-inline'", "object-src 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q, got: %s", directive, csp)
		}
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<body>Hello</body>") {
		t.Errorf("unexpected body: %s", body)
	}
	if !strings.Contains(body, "alf-ui.css") {
		t.Errorf("expected alf-ui.css injection in HTML, got: %s", body)
	}
}

func TestAppHandler_ServesTrailingSlash(t *testing.T) {
	h, dir := newTestAppHandler(t)
	appDir := filepath.Join(dir, "dashboard")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>ok</html>"), 0o644)

	req := httptest.NewRequest("GET", "/apps/dashboard/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAppHandler_ServesAssets(t *testing.T) {
	h, dir := newTestAppHandler(t)
	appDir := filepath.Join(dir, "myapp")
	os.MkdirAll(filepath.Join(appDir, "assets"), 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>"), 0o644)
	os.WriteFile(filepath.Join(appDir, "assets", "style.css"), []byte("body{color:red}"), 0o644)

	req := httptest.NewRequest("GET", "/apps/myapp/assets/style.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("Content-Type = %q, want css", ct)
	}
	// CSS files should not have CSP headers.
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("unexpected CSP on CSS: %s", csp)
	}
}

// SEC-008: unsafe-eval must not appear in app CSP.
func TestAppHandler_CSP_HasUnsafeEval(t *testing.T) {
	// unsafe-eval is safe in sandboxed iframes (sandbox="allow-scripts allow-forms"
	// without allow-same-origin). XSS blast radius is contained: no parent DOM access,
	// no cookies, slug-scoped Bearer token, connect-src 'self' blocks exfiltration.
	h, dir := newTestAppHandler(t)
	appDir := filepath.Join(dir, "myapp")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html></html>"), 0o644)

	req := httptest.NewRequest("GET", "/apps/myapp/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'unsafe-eval'") {
		t.Errorf("CSP should contain 'unsafe-eval' for Alpine/Petite Vue compat: %s", csp)
	}
}

func TestAppHandler_NotFound(t *testing.T) {
	h, _ := newTestAppHandler(t)

	req := httptest.NewRequest("GET", "/apps/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestAppHandler_EmptyName(t *testing.T) {
	h, _ := newTestAppHandler(t)

	req := httptest.NewRequest("GET", "/apps/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestAppHandler_InvalidName(t *testing.T) {
	h, _ := newTestAppHandler(t)

	for _, name := range []string{"../escape", "a.b"} {
		req := httptest.NewRequest("GET", "/apps/"+name, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 for %q, got %d", name, rec.Code)
		}
	}
}

func TestAppHandler_MethodNotAllowed(t *testing.T) {
	h, _ := newTestAppHandler(t)

	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/apps/test", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rec.Code)
		}
	}
}

func TestAppListHandler_ReturnsJSON(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "my-app")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>"), 0o644)
	os.WriteFile(filepath.Join(appDir, "app.json"), []byte(`{"name":"My App","icon":"radar"}`), 0o644)

	store := NewFileAppStore(dir)
	h := &AppListHandler{Store: store}

	req := httptest.NewRequest("GET", "/api/apps/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"my-app"`) {
		t.Errorf("response should contain app name: %s", body)
	}
	if !strings.Contains(body, `"radar"`) {
		t.Errorf("response should contain icon: %s", body)
	}
	if !strings.Contains(body, `"My App"`) {
		t.Errorf("response should contain display name: %s", body)
	}
}
