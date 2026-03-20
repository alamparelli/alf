package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// makeTarGz builds a tar.gz in memory with the given files.
func makeTarGz(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return &buf
}

func makeTarGzWithMode(t *testing.T, files map[string]struct {
	content []byte
	mode    int64
}) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, f := range files {
		hdr := &tar.Header{Name: name, Mode: f.mode, Size: int64(len(f.content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.content); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return &buf
}

func TestExtractBundle_BasicFiles(t *testing.T) {
	dir := t.TempDir()
	bundle := makeTarGz(t, map[string][]byte{
		"manifest.json": []byte(`{"slug":"test"}`),
		"index.html":    []byte(`<html></html>`),
		"app.json":      []byte(`{"name":"Test"}`),
	})

	if err := extractBundle(bundle, dir); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"manifest.json", "index.html", "app.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist", name)
		}
	}

	data, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if string(data) != `{"slug":"test"}` {
		t.Errorf("manifest content = %q", data)
	}
}

func TestExtractBundle_SkipsDataDir(t *testing.T) {
	dir := t.TempDir()

	// Pre-create data/user.db to simulate existing user data.
	os.MkdirAll(filepath.Join(dir, "data"), 0755)
	os.WriteFile(filepath.Join(dir, "data", "user.db"), []byte("precious"), 0644)

	bundle := makeTarGz(t, map[string][]byte{
		"manifest.json":  []byte(`{}`),
		"data/app.db":    []byte("should be skipped"),
		"data/port":      []byte("9999"),
	})

	if err := extractBundle(bundle, dir); err != nil {
		t.Fatal(err)
	}

	// User data should be preserved.
	data, _ := os.ReadFile(filepath.Join(dir, "data", "user.db"))
	if string(data) != "precious" {
		t.Error("user data was overwritten")
	}

	// Bundle's data/ files should NOT be extracted.
	if _, err := os.Stat(filepath.Join(dir, "data", "app.db")); err == nil {
		t.Error("data/app.db should not have been extracted")
	}
}

func TestExtractBundle_SkipsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	bundle := makeTarGz(t, map[string][]byte{
		"../evil.txt":    []byte("bad"),
		"manifest.json":  []byte(`{}`),
	})

	if err := extractBundle(bundle, dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "..", "evil.txt")); err == nil {
		t.Error("path traversal file should not have been extracted")
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Error("legitimate file should have been extracted")
	}
}

func TestExtractBundle_PreservesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	bundle := makeTarGzWithMode(t, map[string]struct {
		content []byte
		mode    int64
	}{
		"bin/myapp": {content: []byte("binary"), mode: 0755},
		"app.json":  {content: []byte(`{}`), mode: 0644},
	})

	if err := extractBundle(bundle, dir); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "bin", "myapp"))
	if err != nil {
		t.Fatal("bin/myapp not found")
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("executable bit not preserved: %v", info.Mode())
	}

	info2, _ := os.Stat(filepath.Join(dir, "app.json"))
	if info2.Mode()&0111 != 0 {
		t.Errorf("non-executable should not have execute bit: %v", info2.Mode())
	}
}

func TestExtractBundle_TooManyFiles(t *testing.T) {
	files := make(map[string][]byte)
	for i := 0; i < 101; i++ {
		files[filepath.Join("files", string(rune('a'+i/26))+string(rune('a'+i%26)))] = []byte("x")
	}
	bundle := makeTarGz(t, files)
	dir := t.TempDir()

	err := extractBundle(bundle, dir)
	if err == nil {
		t.Error("expected error for too many files")
	}
}

func TestExtractBundle_CreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	bundle := makeTarGz(t, map[string][]byte{
		"skills/my-skill/SKILL.md": []byte("# Skill"),
		"bin/tool":                 []byte("binary"),
	})

	if err := extractBundle(bundle, dir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "skills", "my-skill", "SKILL.md"))
	if err != nil {
		t.Fatal("nested file not found")
	}
	if string(data) != "# Skill" {
		t.Errorf("content = %q", data)
	}
}

func TestExtractBundle_SkipsSymlinks(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// Add a symlink.
	tw.WriteHeader(&tar.Header{
		Name:     "evil-link",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	})
	// Add a regular file.
	content := []byte(`{"ok":true}`)
	tw.WriteHeader(&tar.Header{Name: "app.json", Mode: 0644, Size: int64(len(content))})
	tw.Write(content)

	tw.Close()
	gz.Close()

	dir := t.TempDir()
	if err := extractBundle(&buf, dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(dir, "evil-link")); err == nil {
		t.Error("symlink should not have been extracted")
	}
	if _, err := os.Stat(filepath.Join(dir, "app.json")); err != nil {
		t.Error("regular file should have been extracted")
	}
}
