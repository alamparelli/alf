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

func TestInstallActivatesApp(t *testing.T) {
	base := setupTestEnv(t, "testapp")
	m := NewManager(base)

	// Simulate install (sets state + activates).
	m.mu.Lock()
	m.states["testapp"] = StateInstalled
	m.activate("testapp")
	m.saveState()
	m.mu.Unlock()

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

	// Verify state is installed.
	apps := m.List()
	found := false
	for _, a := range apps {
		if a.Slug == "testapp" {
			found = true
			if a.State != StateInstalled {
				t.Fatalf("expected state %q, got %q", StateInstalled, a.State)
			}
		}
	}
	if !found {
		t.Fatal("testapp not found in List")
	}
}

func TestRestoreInstalled(t *testing.T) {
	base := setupTestEnv(t, "testapp")

	m1 := NewManager(base)
	m1.mu.Lock()
	m1.states["testapp"] = StateInstalled
	m1.activate("testapp")
	m1.saveState()
	m1.mu.Unlock()

	// Remove symlinks to simulate restart.
	os.Remove(filepath.Join(base, "tools", "testapp-action"))
	os.Remove(filepath.Join(base, "tools", "testapp-action.json"))

	// Simulate restart: create a fresh Manager and call RestoreInstalled.
	m2 := NewManager(base)
	if err := m2.RestoreInstalled(); err != nil {
		t.Fatalf("RestoreInstalled: %v", err)
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

	// Simulate marketplace install for both.
	m.mu.Lock()
	m.states["alpha"] = StateInstalled
	m.states["beta"] = StateInstalled
	m.saveState()
	m.mu.Unlock()

	apps := m.List()
	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(apps))
	}

	for _, a := range apps {
		if a.State != StateInstalled {
			t.Errorf("%s: expected %q, got %q", a.Slug, StateInstalled, a.State)
		}
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

	// Install + activate.
	m.mu.Lock()
	m.states["testapp"] = StateInstalled
	m.activate("testapp")
	m.saveState()
	m.mu.Unlock()

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

func TestFullLifecycle(t *testing.T) {
	base := setupTestEnv(t, "lifecycle")
	m := NewManager(base)

	toolSym := filepath.Join(base, "tools", "lifecycle-action")
	toolSchema := filepath.Join(base, "tools", "lifecycle-action.json")

	// 1. Install + activate — tools created, state=installed
	m.mu.Lock()
	m.states["lifecycle"] = StateInstalled
	m.activate("lifecycle")
	m.saveState()
	m.mu.Unlock()

	if _, err := os.Lstat(toolSym); err != nil {
		t.Fatalf("tool symlink missing after install: %v", err)
	}
	if _, err := os.Stat(toolSchema); err != nil {
		t.Fatalf("tool schema missing after install: %v", err)
	}

	// 2. Uninstall — everything cleaned
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

func TestOnChange(t *testing.T) {
	base := setupTestEnv(t, "testapp")

	m := NewManager(base)

	calls := 0
	m.SetOnChange(func() {
		calls++
	})

	// Install + activate triggers onChange.
	m.mu.Lock()
	m.states["testapp"] = StateInstalled
	m.activate("testapp")
	m.saveState()
	if m.onChange != nil {
		m.onChange()
	}
	m.mu.Unlock()

	if calls != 1 {
		t.Fatalf("expected onChange called 1 time after install, got %d", calls)
	}

	if err := m.Uninstall("testapp"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected onChange called 2 times after uninstall, got %d", calls)
	}
}

// TestUninstallRemovesAppDir verifies that Uninstall removes the app directory
// entirely when there is no data/ subdirectory to preserve. Regression for
// issue #277: leftover empty dirs showed up in the Developer Source App list.
func TestUninstallRemovesAppDir(t *testing.T) {
	base := setupTestEnv(t, "ghostapp")
	m := NewManager(base)

	m.mu.Lock()
	m.states["ghostapp"] = StateInstalled
	m.activate("ghostapp")
	m.saveState()
	m.mu.Unlock()

	if err := m.Uninstall("ghostapp"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// App directory should be gone entirely (no data/ to preserve).
	appDir := filepath.Join(base, "apps", "ghostapp")
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Fatalf("expected app dir removed, got err: %v", err)
	}
}

// TestUninstallPreservesDataDir verifies that Uninstall keeps the app dir and
// data/ contents intact when the user has data in apps/<slug>/data/.
func TestUninstallPreservesDataDir(t *testing.T) {
	base := setupTestEnv(t, "dataapp")

	dataDir := filepath.Join(base, "apps", "dataapp", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dataDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(base)
	m.mu.Lock()
	m.states["dataapp"] = StateInstalled
	m.activate("dataapp")
	m.saveState()
	m.mu.Unlock()

	if err := m.Uninstall("dataapp"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected data/ preserved: %v", err)
	}
}

// TestUninstallWithBrokenManifest verifies that Uninstall cleans up residual
// tool symlinks, schemas and app-dir contents even when the manifest has been
// deleted or corrupted. Regression for issues #250 and #277: a missing
// manifest caused deactivate() to error out and Uninstall to abort, leaving
// orphaned files behind.
func TestUninstallWithBrokenManifest(t *testing.T) {
	base := setupTestEnv(t, "brokenapp")
	m := NewManager(base)

	// Install + activate normally so tool symlinks/schemas get created.
	m.mu.Lock()
	m.states["brokenapp"] = StateInstalled
	m.activate("brokenapp")
	m.saveState()
	m.mu.Unlock()

	toolSym := filepath.Join(base, "tools", "brokenapp-action")
	toolSchema := filepath.Join(base, "tools", "brokenapp-action.json")
	if _, err := os.Lstat(toolSym); err != nil {
		t.Fatalf("tool symlink should exist after activate: %v", err)
	}

	// Corrupt the manifest so deactivate() cannot enumerate tools.
	if err := os.WriteFile(
		filepath.Join(base, "apps", "brokenapp", "manifest.json"),
		[]byte("not json"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// Uninstall must succeed and still scrub the orphan tool files.
	if err := m.Uninstall("brokenapp"); err != nil {
		t.Fatalf("Uninstall with broken manifest must not error: %v", err)
	}

	if _, err := os.Lstat(toolSym); !os.IsNotExist(err) {
		t.Fatalf("tool symlink should be cleaned up even with broken manifest, got err: %v", err)
	}
	if _, err := os.Stat(toolSchema); !os.IsNotExist(err) {
		t.Fatalf("tool schema should be cleaned up even with broken manifest, got err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "apps", "brokenapp")); !os.IsNotExist(err) {
		t.Fatalf("app dir should be removed even with broken manifest, got err: %v", err)
	}

	// Manager state entries must all be cleared.
	m.mu.Lock()
	_, hasState := m.states["brokenapp"]
	_, hasPerms := m.perms["brokenapp"]
	_, hasTrusted := m.trusted["brokenapp"]
	m.mu.Unlock()
	if hasState || hasPerms || hasTrusted {
		t.Fatalf("expected all manager state cleared, got state=%v perms=%v trusted=%v",
			hasState, hasPerms, hasTrusted)
	}
}
