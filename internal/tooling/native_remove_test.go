package tooling

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveNativeTool_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello"), 0644)

	tool := RemoveNativeTool{DataDir: dir}
	result, err := tool.Run(context.Background(), `{"path":"test.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("file should be deleted")
	}
	if result != "Deleted file: "+f {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestRemoveNativeTool_DeleteDirRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(filepath.Join(sub, "nested"), 0755)
	os.WriteFile(filepath.Join(sub, "nested", "file.txt"), []byte("data"), 0644)

	tool := RemoveNativeTool{DataDir: dir}
	result, err := tool.Run(context.Background(), `{"path":"subdir","recursive":true}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatal("directory should be deleted")
	}
	if result != "Deleted directory: "+sub {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestRemoveNativeTool_DirWithoutRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0755)

	tool := RemoveNativeTool{DataDir: dir}
	_, err := tool.Run(context.Background(), `{"path":"subdir"}`)
	if err == nil {
		t.Fatal("expected error for directory without recursive flag")
	}
}

func TestRemoveNativeTool_ProtectedPath(t *testing.T) {
	tool := RemoveNativeTool{}
	_, err := tool.Run(context.Background(), `{"path":"/","recursive":true}`)
	if err == nil {
		t.Fatal("expected error for protected path")
	}
}

func TestRemoveNativeTool_OutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	tool := RemoveNativeTool{DataDir: dir}
	_, err := tool.Run(context.Background(), `{"path":"/etc/passwd"}`)
	if err == nil {
		t.Fatal("expected error for path outside workspace")
	}
}

func TestRemoveNativeTool_NonExistent(t *testing.T) {
	dir := t.TempDir()
	tool := RemoveNativeTool{DataDir: dir}
	_, err := tool.Run(context.Background(), `{"path":"nope.txt"}`)
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestRemoveNativeTool_DataRoot(t *testing.T) {
	dir := t.TempDir()
	tool := RemoveNativeTool{DataDir: dir}
	_, err := tool.Run(context.Background(), `{"path":"` + dir + `","recursive":true}`)
	if err == nil {
		t.Fatal("expected error for data root deletion")
	}
}
