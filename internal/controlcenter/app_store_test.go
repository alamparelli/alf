package controlcenter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileAppStore_ListEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewFileAppStore(dir)

	apps, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(apps))
	}
}

func TestFileAppStore_ListIgnoresNonDirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "stray.html"), []byte("<html>"), 0o644)

	store := NewFileAppStore(dir)
	apps, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(apps))
	}
}

func TestFileAppStore_ListRequiresIndexHTML(t *testing.T) {
	dir := t.TempDir()
	// Dir without index.html should be ignored.
	os.MkdirAll(filepath.Join(dir, "no-index"), 0o755)
	os.WriteFile(filepath.Join(dir, "no-index", "readme.md"), []byte("hi"), 0o644)

	store := NewFileAppStore(dir)
	apps, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(apps))
	}
}

func TestFileAppStore_ListWithAppJSON(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "my-app")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>hello</html>"), 0o644)
	os.WriteFile(filepath.Join(appDir, "app.json"), []byte(`{"name":"My App","icon":"radar","description":"A test app"}`), 0o644)

	store := NewFileAppStore(dir)
	apps, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Name != "my-app" {
		t.Errorf("name = %q, want my-app", apps[0].Name)
	}
	if apps[0].DisplayName != "My App" {
		t.Errorf("display_name = %q, want My App", apps[0].DisplayName)
	}
	if apps[0].Icon != "radar" {
		t.Errorf("icon = %q, want radar", apps[0].Icon)
	}
	if apps[0].Description != "A test app" {
		t.Errorf("description = %q, want A test app", apps[0].Description)
	}
}

func TestFileAppStore_ListWithoutAppJSON(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "simple")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>hi</html>"), 0o644)

	store := NewFileAppStore(dir)
	apps, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].DisplayName != "" {
		t.Errorf("expected empty display_name, got %q", apps[0].DisplayName)
	}
	if apps[0].Icon != "" {
		t.Errorf("expected empty icon, got %q", apps[0].Icon)
	}
}

func TestFileAppStore_ReadFile(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "test-app")
	os.MkdirAll(filepath.Join(appDir, "assets"), 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>test</html>"), 0o644)
	os.WriteFile(filepath.Join(appDir, "assets", "style.css"), []byte("body{}"), 0o644)

	store := NewFileAppStore(dir)

	data, err := store.ReadFile("test-app", "index.html")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "<html>test</html>" {
		t.Errorf("unexpected content: %s", data)
	}

	data, err = store.ReadFile("test-app", "assets/style.css")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "body{}" {
		t.Errorf("unexpected content: %s", data)
	}
}

func TestFileAppStore_ReadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "test-app")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>"), 0o644)

	store := NewFileAppStore(dir)

	_, err := store.ReadFile("test-app", "nonexistent.js")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileAppStore_ReadFilePathTraversal(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "test-app")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "index.html"), []byte("<html>"), 0o644)
	// Create a file outside the app dir.
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0o644)

	store := NewFileAppStore(dir)

	_, err := store.ReadFile("test-app", "../secret.txt")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestFileAppStore_ReadFileInvalidAppName(t *testing.T) {
	dir := t.TempDir()
	store := NewFileAppStore(dir)

	_, err := store.ReadFile("../escape", "index.html")
	if err == nil {
		t.Error("expected error for invalid app name")
	}
}
