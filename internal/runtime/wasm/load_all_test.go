package wasm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateLegacyBundles_MovesWASMToolAndApp pins the §4.1 path
// migration (#420): bundles found in <dataDir>/skills.d/wasm/<id>/
// are moved to <dataDir>/tools/<id>/ (wasm-tool) or
// <dataDir>/apps/<id>/ (wasm-app) based on manifest kind. The migrator
// reads the canonical id from the manifest and uses it as the new
// directory name.
func TestMigrateLegacyBundles_MovesWASMToolAndApp(t *testing.T) {
	dataDir := t.TempDir()
	legacy := filepath.Join(dataDir, "skills.d", "wasm")

	plantBundle(t, legacy, "notes-counter", "wasm-tool")
	plantBundle(t, legacy, "todo-app", "wasm-app")

	l := &Loader{Logger: func(string, ...any) {}}
	errs := l.migrateLegacyBundles(dataDir)
	if len(errs) != 0 {
		t.Fatalf("migrate errors: %v", errs)
	}

	// notes-counter (wasm-tool) → tools/notes-counter/
	if _, err := os.Stat(filepath.Join(dataDir, "tools", "notes-counter", "manifest.toml")); err != nil {
		t.Errorf("wasm-tool not migrated: %v", err)
	}
	// todo-app (wasm-app) → apps/todo-app/
	if _, err := os.Stat(filepath.Join(dataDir, "apps", "todo-app", "manifest.toml")); err != nil {
		t.Errorf("wasm-app not migrated: %v", err)
	}
	// legacy root removed (was emptied)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy root should be removed: %v", err)
	}
}

// TestMigrateLegacyBundles_Idempotent ensures the migration is safe
// to re-run. A second call finds bundles already at the destination
// and skips them without error.
func TestMigrateLegacyBundles_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	plantBundle(t, filepath.Join(dataDir, "skills.d", "wasm"), "stable-tool", "wasm-tool")

	l := &Loader{Logger: func(string, ...any) {}}
	if errs := l.migrateLegacyBundles(dataDir); len(errs) != 0 {
		t.Fatalf("first migrate: %v", errs)
	}
	// Re-plant the legacy bundle as if a partial install put it back.
	plantBundle(t, filepath.Join(dataDir, "skills.d", "wasm"), "stable-tool", "wasm-tool")
	if errs := l.migrateLegacyBundles(dataDir); len(errs) != 0 {
		t.Fatalf("second migrate: %v", errs)
	}
	// Destination is intact (the second call must NOT clobber it).
	if _, err := os.Stat(filepath.Join(dataDir, "tools", "stable-tool", "manifest.toml")); err != nil {
		t.Errorf("destination missing after idempotent re-run: %v", err)
	}
}

// TestMigrateLegacyBundles_SkipsForbiddenKind ensures bundles with a
// kind outside the wasm-tool/wasm-app set are left in place; the loader
// scan will refuse them downstream rather than migrating into the
// admitted paths under a forbidden kind.
func TestMigrateLegacyBundles_SkipsForbiddenKind(t *testing.T) {
	dataDir := t.TempDir()
	plantBundle(t, filepath.Join(dataDir, "skills.d", "wasm"), "ghost", "marketplace-app")

	l := &Loader{Logger: func(string, ...any) {}}
	if errs := l.migrateLegacyBundles(dataDir); len(errs) != 0 {
		t.Fatalf("migrate errors: %v", errs)
	}
	// Source still present, destination NOT created.
	if _, err := os.Stat(filepath.Join(dataDir, "skills.d", "wasm", "ghost", "manifest.toml")); err != nil {
		t.Errorf("forbidden-kind bundle should stay in legacy root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "apps", "ghost")); !os.IsNotExist(err) {
		t.Errorf("forbidden-kind bundle leaked into apps/: %v", err)
	}
}

// TestMigrateLegacyBundles_NoLegacyDir is a clean-install scenario —
// no skills.d/wasm/ exists. The migrator must return cleanly without
// errors and without creating any directories.
func TestMigrateLegacyBundles_NoLegacyDir(t *testing.T) {
	dataDir := t.TempDir()
	l := &Loader{Logger: func(string, ...any) {}}
	if errs := l.migrateLegacyBundles(dataDir); len(errs) != 0 {
		t.Fatalf("clean install migrate produced errors: %v", errs)
	}
}

// plantBundle writes a minimal bundle (manifest.toml + stub <id>.wasm)
// at the given root. Helper for migration tests — the bundle is not
// instantiable, only sufficient for the migrator's manifest read.
func plantBundle(t *testing.T, root, id, kind string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "alf_envelope_version = 1\n" +
		"id      = \"" + id + "\"\n" +
		"kind    = \"" + kind + "\"\n" +
		"version = \"0.1.0\"\n" +
		"name    = \"" + id + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty .wasm so file-tree shape matches a real bundle; migration
	// doesn't read its content.
	if err := os.WriteFile(filepath.Join(dir, id+".wasm"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
