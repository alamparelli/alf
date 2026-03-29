package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockAppServer creates a local HTTP server that simulates an app REST backend.
func mockAppServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func setupAppProxy(t *testing.T, port string) (*AppHandler, string) {
	t.Helper()
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "later")
	os.MkdirAll(filepath.Join(appsDir, "data"), 0o755)
	os.WriteFile(filepath.Join(appsDir, "data", "port"), []byte(port), 0o644)
	os.WriteFile(filepath.Join(appsDir, "index.html"), []byte("<html>test</html>"), 0o644)
	return &AppHandler{Store: NewFileAppStore(dir)}, dir
}

func TestAppProxy_ForwardsGET(t *testing.T) {
	srv := mockAppServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/items" {
			t.Errorf("expected /api/items, got %s", r.URL.Path)
		}
		if r.URL.RawQuery != "status=active" {
			t.Errorf("expected query status=active, got %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":["a","b"]}`))
	})
	defer srv.Close()

	// Extract port from test server URL
	port := srv.URL[len("http://127.0.0.1:"):]
	h, _ := setupAppProxy(t, port)

	req := httptest.NewRequest("GET", "/apps/later/api/items?status=active", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	items := resp["items"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestAppProxy_ForwardsPOST(t *testing.T) {
	srv := mockAppServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		w.Write([]byte(`{"ok":true,"received":"` + string(body) + `"}`))
	})
	defer srv.Close()

	port := srv.URL[len("http://127.0.0.1:"):]
	h, _ := setupAppProxy(t, port)

	req := httptest.NewRequest("POST", "/apps/later/api/items", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAppProxy_NoCookieForwarded(t *testing.T) {
	srv := mockAppServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Errorf("cookies should not be forwarded to app server")
		}
		w.Write([]byte(`{"ok":true}`))
	})
	defer srv.Close()

	port := srv.URL[len("http://127.0.0.1:"):]
	h, _ := setupAppProxy(t, port)

	req := httptest.NewRequest("GET", "/apps/later/api/items", nil)
	req.Header.Set("Cookie", "cc_session=secret123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAppProxy_NoPortFile(t *testing.T) {
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "noserver")
	os.MkdirAll(appsDir, 0o755)
	h := &AppHandler{Store: NewFileAppStore(dir)}

	req := httptest.NewRequest("GET", "/apps/noserver/api/items", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

func TestAppProxy_InvalidPort(t *testing.T) {
	for _, port := range []string{"not-a-number", "80", "0", "99999"} {
		h, _ := setupAppProxy(t, port)
		req := httptest.NewRequest("GET", "/apps/later/api/items", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("port %q: expected 502, got %d", port, rec.Code)
		}
	}
}

func TestAppProxy_ServerDown(t *testing.T) {
	// Port with nothing listening
	h, _ := setupAppProxy(t, "19999")
	req := httptest.NewRequest("GET", "/apps/later/api/items", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

func TestAppProxy_StaticStillWorks(t *testing.T) {
	h, _ := setupAppProxy(t, "9999")

	// index.html should still be served
	req := httptest.NewRequest("GET", "/apps/later/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 for index.html, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<html>") {
		t.Errorf("expected HTML content, got %s", rec.Body.String())
	}
}

func TestAppProxy_CrossAppBlocked(t *testing.T) {
	// App "evil" tries to access "later"'s API — blocked because
	// the proxy only reads the port from the slug in the URL path.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "evil"), 0o755)
	// evil has no data/port → 502
	h := &AppHandler{Store: NewFileAppStore(dir)}

	req := httptest.NewRequest("GET", "/apps/evil/api/items", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}
