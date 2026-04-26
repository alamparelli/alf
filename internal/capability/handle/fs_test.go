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
	inst := NewInstance(context.Background(), capability.ID("cap"), Grants{
		FS: NewFSHandle("cap", dir, FSScope{Reads: []string{"hello.txt"}}),
	})
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

	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Reads: []string{"allowed.txt"}}),
	})
	defer inst.Close()

	if _, err := inst.FS.Read(context.Background(), other); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
}

func TestFSHandle_WriteInScope(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Writes: []string{"out.txt"}}),
	})
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
	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Reads: []string{"x.txt"}}),
	})

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

// TestFSHandle_RefusesSymlinkRead pins SEC-006: a symlink installed
// inside the read scope must NOT be followed. os.ReadFile would
// transparently dereference it and leak the target's contents (which
// could be outside scope). The fix opens with O_NOFOLLOW + Lstat
// pre-check.
func TestFSHandle_RefusesSymlinkRead(t *testing.T) {
	dir := t.TempDir()
	// Create a sensitive target outside scope.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("SENSITIVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Plant a symlink "leak" inside the cap's read scope pointing to
	// the outside target.
	leak := filepath.Join(dir, "leak.txt")
	if err := os.Symlink(outside, leak); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Reads: []string{"leak.txt"}}),
	})
	defer inst.Close()

	data, err := inst.FS.Read(context.Background(), leak)
	if !errors.Is(err, ErrSymlinkRefused) {
		t.Fatalf("symlink read should return ErrSymlinkRefused, got data=%q err=%v", data, err)
	}
	if len(data) != 0 {
		t.Errorf("symlink-target bytes leaked: %q", data)
	}
}

// TestFSHandle_RefusesSymlinkWrite pins SEC-006 on the write side:
// a symlink in scope pointing at an arbitrary target must NOT be
// followed by os.WriteFile (which would clobber the target).
func TestFSHandle_RefusesSymlinkWrite(t *testing.T) {
	dir := t.TempDir()
	// Sensitive target outside scope.
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "important.conf")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Plant a symlink in scope pointing at the outside target.
	leak := filepath.Join(dir, "out.txt")
	if err := os.Symlink(target, leak); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Writes: []string{"out.txt"}}),
	})
	defer inst.Close()

	err := inst.FS.Write(context.Background(), leak, []byte("CLOBBERED"))
	if !errors.Is(err, ErrSymlinkRefused) {
		t.Fatalf("symlink write should return ErrSymlinkRefused, got %v", err)
	}
	// Confirm the outside file was NOT clobbered.
	got, _ := os.ReadFile(target)
	if string(got) != "ORIGINAL" {
		t.Errorf("outside target was clobbered: %q", got)
	}
}

// TestFSHandle_WriteUses0o600 pins SEC-006 mode tightening: written
// files must be 0o600 (owner read/write only), not the previous
// 0o644 which left files world-readable inside shared-volume
// containers.
func TestFSHandle_WriteUses0o600(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Writes: []string{"out.txt"}}),
	})
	defer inst.Close()

	if err := inst.FS.Write(context.Background(), target, []byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %#o, want 0o600", perm)
	}
}

func TestFSHandle_DirectoryScope(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "data")
	_ = os.MkdirAll(sub, 0o755)
	f1 := filepath.Join(sub, "a.txt")
	_ = os.WriteFile(f1, []byte("1"), 0o644)

	inst := NewInstance(context.Background(), "cap", Grants{
		FS: NewFSHandle("cap", dir, FSScope{Reads: []string{"data/"}}),
	})
	defer inst.Close()

	if _, err := inst.FS.Read(context.Background(), f1); err != nil {
		t.Fatalf("dir-scoped read failed: %v", err)
	}
}
