package marketplace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupTestEnv creates a temp directory with the marketplace structure:
//
//	base/
//	  apps/{slug}/manifest.json
//	  apps/{slug}/bin/{slug}   (empty executable)
//	  tools/
func setupTestEnv(t *testing.T, slugs ...string) string {
	t.Helper()
	base := t.TempDir()

	if err := os.MkdirAll(filepath.Join(base, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, slug := range slugs {
		appDir := filepath.Join(base, "apps", slug)
		binDir := filepath.Join(appDir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}

		manifest := Manifest{
			Name:    "Test App",
			Slug:    slug,
			Version: "0.1.0",
			Tools: []ToolDecl{
				{
					Name:        slug + "-action",
					Description: "test",
					Action:      "action",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"input": map[string]any{"type": "string"},
						},
						"required": []any{"input"},
					},
				},
			},
		}
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(appDir, "manifest.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		// Empty executable binary.
		binPath := filepath.Join(binDir, slug)
		if err := os.WriteFile(binPath, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return base
}

func TestNewManager(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(base)
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
}

func TestEnableDisable(t *testing.T) {
	base := setupTestEnv(t, "testapp")
	m := NewManager(base)

	// Enable
	if err := m.Enable("testapp"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// Symlink should exist in tools/.
	symlinkPath := filepath.Join(base, "tools", "testapp-action")
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Fatalf("expected symlink at %s: %v", symlinkPath, err)
	}

	// Schema file should exist in tools/.
	schemaPath := filepath.Join(base, "tools", "testapp-action.json")
	if _, err := os.Stat(schemaPath); err != nil {
		t.Fatalf("expected schema file at %s: %v", schemaPath, err)
	}

	// Verify state is enabled.
	apps := m.List()
	found := false
	for _, a := range apps {
		if a.Slug == "testapp" {
			found = true
			if a.State != StateEnabled {
				t.Fatalf("expected state %q, got %q", StateEnabled, a.State)
			}
		}
	}
	if !found {
		t.Fatal("testapp not found in List")
	}

	// Disable
	if err := m.Disable("testapp"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	// Symlink should be gone.
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Fatalf("expected symlink removed, got err: %v", err)
	}

	// Schema should be gone.
	if _, err := os.Stat(schemaPath); !os.IsNotExist(err) {
		t.Fatalf("expected schema removed, got err: %v", err)
	}

	// Verify state is disabled.
	apps = m.List()
	for _, a := range apps {
		if a.Slug == "testapp" && a.State != StateDisabled {
			t.Fatalf("expected state %q, got %q", StateDisabled, a.State)
		}
	}
}

func TestRestoreEnabled(t *testing.T) {
	base := setupTestEnv(t, "testapp")

	m1 := NewManager(base)
	if err := m1.Enable("testapp"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// Remove symlinks to simulate restart.
	os.Remove(filepath.Join(base, "tools", "testapp-action"))
	os.Remove(filepath.Join(base, "tools", "testapp-action.json"))

	// Simulate restart: create a fresh Manager and call RestoreEnabled.
	m2 := NewManager(base)
	if err := m2.RestoreEnabled(); err != nil {
		t.Fatalf("RestoreEnabled: %v", err)
	}

	symlinkPath := filepath.Join(base, "tools", "testapp-action")
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Fatalf("expected symlink restored at %s: %v", symlinkPath, err)
	}

	schemaPath := filepath.Join(base, "tools", "testapp-action.json")
	if _, err := os.Stat(schemaPath); err != nil {
		t.Fatalf("expected schema restored at %s: %v", schemaPath, err)
	}
}

func TestList(t *testing.T) {
	base := setupTestEnv(t, "alpha", "beta")

	m := NewManager(base)

	// Simulate marketplace install for both (sets state entry).
	m.mu.Lock()
	m.states["alpha"] = StateInstalled
	m.states["beta"] = StateInstalled
	m.saveState()
	m.mu.Unlock()

	if err := m.Enable("alpha"); err != nil {
		t.Fatalf("Enable alpha: %v", err)
	}

	apps := m.List()
	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(apps))
	}

	states := make(map[string]AppState)
	for _, a := range apps {
		states[a.Slug] = a.State
	}

	if states["alpha"] != StateEnabled {
		t.Errorf("alpha: expected %q, got %q", StateEnabled, states["alpha"])
	}
	if states["beta"] != StateInstalled {
		t.Errorf("beta: expected %q, got %q", StateInstalled, states["beta"])
	}

	// Local app (no state entry) should NOT appear in List().
	localDir := filepath.Join(base, "apps", "localapp")
	os.MkdirAll(localDir, 0o755)
	os.WriteFile(filepath.Join(localDir, "manifest.json"),
		[]byte(`{"slug":"localapp","name":"Local App","version":"1.0.0"}`), 0o644)

	apps = m.List()
	if len(apps) != 2 {
		t.Fatalf("local app should not appear in marketplace list, got %d apps", len(apps))
	}
}

func TestUninstall(t *testing.T) {
	base := setupTestEnv(t, "testapp")

	// Create a data/ directory to verify it's preserved.
	dataDir := filepath.Join(base, "apps", "testapp", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dataDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(base)

	if err := m.Enable("testapp"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := m.Uninstall("testapp"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// bin/ should be removed.
	binDir := filepath.Join(base, "apps", "testapp", "bin")
	if _, err := os.Stat(binDir); !os.IsNotExist(err) {
		t.Fatalf("expected bin/ removed, got err: %v", err)
	}

	// data/ should be preserved.
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected data/ preserved: %v", err)
	}

	// Symlinks should be removed.
	symlinkPath := filepath.Join(base, "tools", "testapp-action")
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Fatalf("expected symlink removed after uninstall, got err: %v", err)
	}
}

func TestInstallDoesNotActivateToolsOrSkills(t *testing.T) {
	base := setupTestEnv(t, "myapp")
	m := NewManager(base)

	// Simulate install by setting state to installed (like Install() does).
	m.mu.Lock()
	m.states["myapp"] = StateInstalled
	m.saveState()
	m.mu.Unlock()

	// Tools should NOT exist after install-only.
	symlinkPath := filepath.Join(base, "tools", "myapp-action")
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Fatalf("expected no tool symlink after install, got: %v", err)
	}
	schemaPath := filepath.Join(base, "tools", "myapp-action.json")
	if _, err := os.Stat(schemaPath); !os.IsNotExist(err) {
		t.Fatalf("expected no tool schema after install, got: %v", err)
	}

	// State should be installed, not enabled.
	apps := m.List()
	for _, a := range apps {
		if a.Slug == "myapp" && a.State != StateInstalled {
			t.Fatalf("expected state %q, got %q", StateInstalled, a.State)
		}
	}
}

func TestFullLifecycle(t *testing.T) {
	base := setupTestEnv(t, "lifecycle")
	m := NewManager(base)

	toolSym := filepath.Join(base, "tools", "lifecycle-action")
	toolSchema := filepath.Join(base, "tools", "lifecycle-action.json")

	// 1. Install — no tools, state=installed
	m.mu.Lock()
	m.states["lifecycle"] = StateInstalled
	m.saveState()
	m.mu.Unlock()

	if _, err := os.Lstat(toolSym); !os.IsNotExist(err) {
		t.Fatal("tools should not exist after install")
	}

	// 2. Enable — tools created, state=enabled
	if err := m.Enable("lifecycle"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if _, err := os.Lstat(toolSym); err != nil {
		t.Fatalf("tool symlink missing after enable: %v", err)
	}
	if _, err := os.Stat(toolSchema); err != nil {
		t.Fatalf("tool schema missing after enable: %v", err)
	}

	// 3. Disable — tools removed, state=disabled
	if err := m.Disable("lifecycle"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := os.Lstat(toolSym); !os.IsNotExist(err) {
		t.Fatal("tool symlink should be removed after disable")
	}
	if _, err := os.Stat(toolSchema); !os.IsNotExist(err) {
		t.Fatal("tool schema should be removed after disable")
	}

	// 4. Re-enable — tools back
	if err := m.Enable("lifecycle"); err != nil {
		t.Fatalf("Re-enable: %v", err)
	}
	if _, err := os.Lstat(toolSym); err != nil {
		t.Fatalf("tool symlink missing after re-enable: %v", err)
	}

	// 5. Disable then Uninstall — everything cleaned
	if err := m.Disable("lifecycle"); err != nil {
		t.Fatalf("Disable before uninstall: %v", err)
	}
	if err := m.Uninstall("lifecycle"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Lstat(toolSym); !os.IsNotExist(err) {
		t.Fatal("tool symlink should be gone after uninstall")
	}
	// App dir should be gone (except data/).
	binDir := filepath.Join(base, "apps", "lifecycle", "bin")
	if _, err := os.Stat(binDir); !os.IsNotExist(err) {
		t.Fatal("bin/ should be removed after uninstall")
	}
}

func TestDisabledAppNotInRestoreEnabled(t *testing.T) {
	base := setupTestEnv(t, "app1", "app2")
	m := NewManager(base)

	// Enable both, then disable app2.
	m.Enable("app1")
	m.Enable("app2")
	m.Disable("app2")

	// Remove all tool symlinks to simulate restart.
	os.Remove(filepath.Join(base, "tools", "app1-action"))
	os.Remove(filepath.Join(base, "tools", "app1-action.json"))

	// RestoreEnabled should only restore app1, not app2.
	m2 := NewManager(base)
	m2.RestoreEnabled()

	if _, err := os.Lstat(filepath.Join(base, "tools", "app1-action")); err != nil {
		t.Fatal("app1 tools should be restored")
	}
	if _, err := os.Lstat(filepath.Join(base, "tools", "app2-action")); !os.IsNotExist(err) {
		t.Fatal("app2 tools should NOT be restored (disabled)")
	}
}

func TestOnChange(t *testing.T) {
	base := setupTestEnv(t, "testapp")

	m := NewManager(base)

	calls := 0
	m.SetOnChange(func() {
		calls++
	})

	if err := m.Enable("testapp"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected onChange called 1 time after Enable, got %d", calls)
	}

	if err := m.Disable("testapp"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected onChange called 2 times after Disable, got %d", calls)
	}
}
