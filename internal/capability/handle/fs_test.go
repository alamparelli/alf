package handle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

func TestFSHandle_ReadInScope(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(target, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := NewInstance(context.Background(), capability.ID("cap"),
		NewFSHandle("cap", dir, FSScope{Reads: []string{"hello.txt"}}))
	defer inst.Close()

	data, err := inst.FS.Read(context.Background(), target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hi" {
		t.Fatalf("want hi, got %q", data)
	}
}

func TestFSHandle_OutOfScope(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed.txt")
	other := filepath.Join(dir, "other.txt")
	_ = os.WriteFile(allowed, []byte("a"), 0o644)
	_ = os.WriteFile(other, []byte("b"), 0o644)

	inst := NewInstance(context.Background(), "cap",
		NewFSHandle("cap", dir, FSScope{Reads: []string{"allowed.txt"}}))
	defer inst.Close()

	if _, err := inst.FS.Read(context.Background(), other); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
}

func TestFSHandle_WriteInScope(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	inst := NewInstance(context.Background(), "cap",
		NewFSHandle("cap", dir, FSScope{Writes: []string{"out.txt"}}))
	defer inst.Close()

	if err := inst.FS.Write(context.Background(), target, []byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "x" {
		t.Fatalf("want x, got %q", b)
	}
}

func TestFSHandle_Revocation(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(target, []byte("y"), 0o644)
	inst := NewInstance(context.Background(), "cap",
		NewFSHandle("cap", dir, FSScope{Reads: []string{"x.txt"}}))

	start := time.Now()
	inst.Close()
	if _, err := inst.FS.Read(context.Background(), target); !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("revocation took %v, want <100ms", elapsed)
	}
}

func TestFSHandle_NonSerializable(t *testing.T) {
	h := NewFSHandle("cap", "/tmp", FSScope{})
	if _, err := json.Marshal(h); err == nil {
		t.Fatal("FSHandle must not be JSON-serializable")
	}
}

func TestFSHandle_DirectoryScope(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "data")
	_ = os.MkdirAll(sub, 0o755)
	f1 := filepath.Join(sub, "a.txt")
	_ = os.WriteFile(f1, []byte("1"), 0o644)

	inst := NewInstance(context.Background(), "cap",
		NewFSHandle("cap", dir, FSScope{Reads: []string{"data/"}}))
	defer inst.Close()

	if _, err := inst.FS.Read(context.Background(), f1); err != nil {
		t.Fatalf("dir-scoped read failed: %v", err)
	}
}
