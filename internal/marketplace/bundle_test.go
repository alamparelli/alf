package marketplace

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// makeZip builds a ZIP in memory with the given files.
func makeZip(t *testing.T, files map[string][]byte) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	b := buf.Bytes()
	return bytes.NewReader(b)
}

func makeZipWithMode(t *testing.T, files map[string]struct {
	content []byte
	mode    os.FileMode
}) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, f := range files {
		hdr := &zip.FileHeader{Name: name}
		hdr.SetMode(f.mode)
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(f.content); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	b := buf.Bytes()
	return bytes.NewReader(b)
}

func TestExtractBundle_BasicFiles(t *testing.T) {
	dir := t.TempDir()
	r := makeZip(t, map[string][]byte{
		"manifest.json": []byte(`{"slug":"test"}`),
		"index.html":    []byte(`<html></html>`),
		"app.json":      []byte(`{"name":"Test"}`),
	})

	if err := extractBundle(r, r.Size(), dir); err != nil {
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

	r := makeZip(t, map[string][]byte{
		"manifest.json": []byte(`{}`),
		"data/app.db":   []byte("should be skipped"),
		"data/port":     []byte("9999"),
	})

	if err := extractBundle(r, r.Size(), dir); err != nil {
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
	r := makeZip(t, map[string][]byte{
		"../evil.txt":   []byte("bad"),
		"manifest.json": []byte(`{}`),
	})

	if err := extractBundle(r, r.Size(), dir); err != nil {
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
	r := makeZipWithMode(t, map[string]struct {
		content []byte
		mode    os.FileMode
	}{
		"bin/myapp": {content: []byte("binary"), mode: 0755},
		"app.json":  {content: []byte(`{}`), mode: 0644},
	})

	if err := extractBundle(r, r.Size(), dir); err != nil {
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
	r := makeZip(t, files)
	dir := t.TempDir()

	err := extractBundle(r, r.Size(), dir)
	if err == nil {
		t.Error("expected error for too many files")
	}
}

func TestExtractBundle_CreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	r := makeZip(t, map[string][]byte{
		"skills/my-skill/SKILL.md": []byte("# Skill"),
		"bin/tool":                 []byte("binary"),
	})

	if err := extractBundle(r, r.Size(), dir); err != nil {
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
