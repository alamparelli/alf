package controlcenter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileResourceStore_ListEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".md")

	items, err := store.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestFileResourceStore_PutGetDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".md")

	// Put
	if err := store.Put("hello", []byte("# Hello\nWorld")); err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	// List
	items, err := store.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "hello" {
		t.Errorf("name: got %q, want 'hello'", items[0].Name)
	}

	// Get
	data, err := store.Get("hello")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if string(data) != "# Hello\nWorld" {
		t.Errorf("content: got %q", string(data))
	}

	// Delete
	if err := store.Delete("hello"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	items, _ = store.List()
	if len(items) != 0 {
		t.Errorf("expected 0 items after delete, got %d", len(items))
	}
}

func TestFileResourceStore_InvalidName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".json")

	badNames := []string{"../escape", "foo/bar", "hello world", ".hidden", "a.b"}
	for _, name := range badNames {
		if err := store.Put(name, []byte("{}")); err == nil {
			t.Errorf("Put(%q) should fail", name)
		}
		if _, err := store.Get(name); err == nil {
			t.Errorf("Get(%q) should fail", name)
		}
		if err := store.Delete(name); err == nil {
			t.Errorf("Delete(%q) should fail", name)
		}
	}
}

func TestFileResourceStore_SizeLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".md")

	big := make([]byte, maxResourceSize+1)
	if err := store.Put("big", big); err == nil {
		t.Error("Put() should reject oversized data")
	}
}

func TestFileResourceStore_GetNotFound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".md")

	if _, err := store.Get("nonexistent"); err == nil {
		t.Error("Get() should fail for missing resource")
	}
}

func TestFileResourceStore_DeleteNotFound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".md")

	if err := store.Delete("nonexistent"); err == nil {
		t.Error("Delete() should fail for missing resource")
	}
}

func TestFileResourceStore_AtomicWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".json")

	if err := store.Put("test", []byte(`{"key":"value"}`)); err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	// tmp file should not exist
	if _, err := os.Stat(filepath.Join(dir, "test.json.tmp")); !os.IsNotExist(err) {
		t.Error("tmp file should be cleaned up")
	}
}

func TestFileResourceStore_SkipsSubdirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resources")
	store := NewFileResourceStore(dir, ".md")

	// Create a subdirectory and a non-matching file
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x"), 0o644)
	store.Put("real", []byte("content"))

	items, err := store.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(items) != 1 || items[0].Name != "real" {
		t.Errorf("expected only 'real', got %v", items)
	}
}
