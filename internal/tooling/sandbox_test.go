package tooling

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePath_Relative(t *testing.T) {
	got := ResolvePath("/data", "file.txt")
	if got != "/data/file.txt" {
		t.Errorf("expected /data/file.txt, got %s", got)
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	got := ResolvePath("/data", "/other/file.txt")
	if got != "/other/file.txt" {
		t.Errorf("expected /other/file.txt, got %s", got)
	}
}

func TestResolvePath_EmptyDataDir(t *testing.T) {
	got := ResolvePath("", "file.txt")
	if got != "file.txt" {
		t.Errorf("expected file.txt, got %s", got)
	}
}

func TestCheckBoundary_EmptyDataDir(t *testing.T) {
	path, err := CheckBoundary("", "/anywhere/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/anywhere/file.txt" {
		t.Errorf("expected passthrough, got %s", path)
	}
}

func TestCheckBoundary_InsideBoundary(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0o755)
	file := filepath.Join(sub, "test.txt")
	os.WriteFile(file, []byte("ok"), 0o644)

	path, err := CheckBoundary(dir, file)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestCheckBoundary_OutsideBoundary(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "escape.txt")

	_, err := CheckBoundary(dir, outside)
	if err == nil {
		t.Error("expected error for path escaping boundary")
	}
}

func TestCheckBoundary_DotDotTraversal(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	_, err := CheckBoundary(dir, filepath.Join(dir, "sub", "..", "..", "etc", "passwd"))
	if err == nil {
		t.Error("expected error for .. traversal escaping boundary")
	}
}

func TestCheckBoundary_NewFileInsideBoundary(t *testing.T) {
	dir := t.TempDir()
	newFile := filepath.Join(dir, "new.txt")

	path, err := CheckBoundary(dir, newFile)
	if err != nil {
		t.Fatalf("expected success for new file, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestCheckBoundary_SymlinkInside(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(target, []byte("data"), 0o644)
	link := filepath.Join(dir, "link.txt")
	os.Symlink(target, link)

	path, err := CheckBoundary(dir, link)
	if err != nil {
		t.Fatalf("expected success for symlink inside boundary, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestCheckBoundary_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	os.WriteFile(target, []byte("secret"), 0o644)

	link := filepath.Join(dir, "escape-link")
	os.Symlink(target, link)

	_, err := CheckBoundary(dir, link)
	if err == nil {
		t.Error("expected error for symlink escaping boundary")
	}
}
