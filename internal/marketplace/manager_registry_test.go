package marketplace

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression lock for step 2 (capability: marketplace + tooling +
// skills) of milestone 0.7.9. Covers the state accessor surface and
// the HTTP registry round-trip (catalog, install, update) so the
// rework cannot silently change the wire contract with the
// alf-marketplace server.

// ----- Simple state accessors -----------------------------------------

func TestIsTracked_TrueAfterManifestLoaded(t *testing.T) {
	base := setupTestEnv(t, "trackedapp")
	m := NewManager(base)

	// The app was seeded on disk — RestoreInstalled cycles states
	// through the real loader. Bypass with direct state write.
	m.mu.Lock()
	m.states["trackedapp"] = StateInstalled
	m.activate("trackedapp")
	m.mu.Unlock()

	if !m.IsTracked("trackedapp") {
		t.Error("installed app must be tracked")
	}
	if m.IsTracked("unknown-app") {
		t.Error("unknown app should not be tracked")
	}
}

func TestMarkTrusted_RecordsFlag(t *testing.T) {
	m := NewManager(t.TempDir())

	if m.trusted["x"] {
		t.Fatal("pre-condition: x should not be trusted yet")
	}
	m.MarkTrusted("x")

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.trusted["x"] {
		t.Error("MarkTrusted did not record the flag")
	}
}

func TestGetServices_ReturnsCopy(t *testing.T) {
	m := NewManager(t.TempDir())

	m.mu.Lock()
	m.services["app1"] = []string{"openrouter", "telegram"}
	m.mu.Unlock()

	got := m.GetServices("app1")
	if len(got) != 2 || got[0] != "openrouter" || got[1] != "telegram" {
		t.Errorf("GetServices mismatch: %+v", got)
	}

	// Mutating the returned slice must not leak into internal state.
	got[0] = "MUTATED"
	second := m.GetServices("app1")
	if second[0] == "MUTATED" {
		t.Error("GetServices leaks internal slice — caller mutation bled into state")
	}
}

func TestGetServices_NilForUnknownApp(t *testing.T) {
	m := NewManager(t.TempDir())
	if got := m.GetServices("nope"); got != nil {
		t.Errorf("unknown app should return nil, got %+v", got)
	}
}

// ----- FetchCatalog ----------------------------------------------------

func TestFetchCatalog_NoURL_ReturnsNilNil(t *testing.T) {
	m := NewManager(t.TempDir())
	// Don't set registryURL — simulates an unconfigured instance.
	apps, err := m.FetchCatalog()
	if err != nil {
		t.Fatalf("FetchCatalog no-URL: unexpected err %v", err)
	}
	if apps != nil {
		t.Errorf("no-URL should return nil slice, got %+v", apps)
	}
}

func TestFetchCatalog_HappyPath(t *testing.T) {
	apps := []RemoteApp{
		{Slug: "todo", Name: "Todo", Version: "1.0.0"},
		{Slug: "notes", Name: "Notes", Version: "0.2.0", Category: "productivity"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/catalog" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Alf-Instance") != "true" {
			t.Error("missing X-Alf-Instance header — registry needs this for instance-vs-browser routing")
		}
		json.NewEncoder(w).Encode(apps)
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.registryURL = srv.URL

	got, err := m.FetchCatalog()
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if len(got) != 2 || got[0].Slug != "todo" || got[1].Slug != "notes" {
		t.Errorf("catalog payload mismatch: %+v", got)
	}
}

func TestFetchCatalog_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.registryURL = srv.URL

	_, err := m.FetchCatalog()
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err should mention status: %v", err)
	}
}

func TestFetchCatalog_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("{not-json"))
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.registryURL = srv.URL

	_, err := m.FetchCatalog()
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

// ----- Install with fake registry bundle ------------------------------

// buildBundle returns a ZIP archive containing the given path→content
// entries, mimicking what alf-marketplace serves.
func buildBundle(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for path, content := range entries {
		w, err := zw.Create(path)
		if err != nil {
			t.Fatalf("zip create %s: %v", path, err)
		}
		io.WriteString(w, content)
	}
	zw.Close()
	return buf.Bytes()
}

func TestInstall_HappyPath_ViaBundle(t *testing.T) {
	manifest := Manifest{
		Name:    "TestTodo",
		Slug:    "todo",
		Version: "1.0.0",
	}
	manifestJSON, _ := json.Marshal(manifest)

	bundle := buildBundle(t, map[string]string{
		"manifest.json": string(manifestJSON),
		"index.html":    "<html>todo</html>",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/apps/todo/bundle") {
			t.Errorf("unexpected install path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Alf-Instance") != "true" {
			t.Error("missing X-Alf-Instance on bundle fetch")
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Write(bundle)
	}))
	defer srv.Close()

	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "tools"), 0o755)
	m := NewManager(base)
	m.registryURL = srv.URL
	// Install calls lockAppFiles which chmods files 0o444 — that breaks
	// t.TempDir cleanup. Restore perms before the test returns.
	t.Cleanup(func() { m.unlockAppFiles("todo") })

	if err := m.Install("todo"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// App files must be written.
	if _, err := os.Stat(filepath.Join(base, "apps", "todo", "manifest.json")); err != nil {
		t.Errorf("manifest.json missing after install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "apps", "todo", "index.html")); err != nil {
		t.Errorf("index.html missing after install: %v", err)
	}

	// State must be installed + trusted (bundle came from registry).
	m.mu.Lock()
	state, ok := m.states["todo"]
	trusted := m.trusted["todo"]
	m.mu.Unlock()
	if !ok || state != StateInstalled {
		t.Errorf("state after install = %q, want %q", state, StateInstalled)
	}
	if !trusted {
		t.Error("app from registry must be marked trusted (SEC-001)")
	}
}

func TestInstall_NoRegistry_Errors(t *testing.T) {
	m := NewManager(t.TempDir())
	// registryURL stays empty.
	err := m.Install("anything")
	if err == nil {
		t.Fatal("expected error when no registry configured")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("error should mention registry: %v", err)
	}
}

func TestInstall_ServerErrorBubbles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "tools"), 0o755)
	m := NewManager(base)
	m.registryURL = srv.URL

	err := m.Install("missing")
	if err == nil {
		t.Fatal("expected error when registry returns 404 for both bundle and legacy")
	}
}

// ----- Update ----------------------------------------------------------

func TestUpdate_NotInstalled_Errors(t *testing.T) {
	m := NewManager(t.TempDir())
	m.registryURL = "http://example.invalid"
	if err := m.Update("never-installed"); err == nil {
		t.Fatal("expected error updating uninstalled app")
	}
}

func TestUpdate_NoRegistry_Errors(t *testing.T) {
	m := NewManager(t.TempDir())
	m.mu.Lock()
	m.states["installed"] = StateInstalled
	m.mu.Unlock()

	err := m.Update("installed")
	if err == nil {
		t.Fatal("expected error when no registry configured")
	}
}

func TestUpdate_PreservesDataDir(t *testing.T) {
	manifest := Manifest{Slug: "persistent", Name: "Persistent", Version: "2.0.0"}
	mj, _ := json.Marshal(manifest)
	bundle := buildBundle(t, map[string]string{
		"manifest.json": string(mj),
		"new-file":      "from-update",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(bundle)
	}))
	defer srv.Close()

	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "tools"), 0o755)
	appDir := filepath.Join(base, "apps", "persistent")
	dataDir := filepath.Join(appDir, "data")
	os.MkdirAll(dataDir, 0o755)
	// Seed the pre-update manifest (required by deactivate → loadManifest),
	// user data that must survive, and an unrelated file that must be wiped.
	oldManifest := Manifest{Slug: "persistent", Name: "Persistent", Version: "1.0.0"}
	oldMJ, _ := json.Marshal(oldManifest)
	os.WriteFile(filepath.Join(appDir, "manifest.json"), oldMJ, 0o644)
	os.WriteFile(filepath.Join(dataDir, "user.db"), []byte("user-payload"), 0o644)
	os.WriteFile(filepath.Join(appDir, "old-file"), []byte("to-be-wiped"), 0o644)

	m := NewManager(base)
	m.registryURL = srv.URL
	m.mu.Lock()
	m.states["persistent"] = StateInstalled
	m.mu.Unlock()
	t.Cleanup(func() { m.unlockAppFiles("persistent") })

	if err := m.Update("persistent"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// data/ must survive.
	data, err := os.ReadFile(filepath.Join(dataDir, "user.db"))
	if err != nil || string(data) != "user-payload" {
		t.Errorf("data/ was wiped on update: err=%v data=%q", err, data)
	}
	// Old top-level file must be gone.
	if _, err := os.Stat(filepath.Join(appDir, "old-file")); !os.IsNotExist(err) {
		t.Error("old app file was not removed on update")
	}
	// New bundle file must exist.
	if _, err := os.Stat(filepath.Join(appDir, "new-file")); err != nil {
		t.Errorf("new bundle file missing: %v", err)
	}
}
