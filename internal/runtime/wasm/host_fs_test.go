package wasm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero/api"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/handle"
)

func TestPackResult_LayoutMatchesABI(t *testing.T) {
	// High 32 bits = err, low 32 bits = out_len per WASM.md §3.2.
	got := packResult(errBufferTooSmall, 1024)
	if gotErr := uint32(got >> 32); gotErr != errBufferTooSmall {
		t.Errorf("high bits: got %d, want %d", gotErr, errBufferTooSmall)
	}
	if gotLen := uint32(got & 0xFFFFFFFF); gotLen != 1024 {
		t.Errorf("low bits: got %d, want %d", gotLen, 1024)
	}
}

func TestPackResult_MaxLen(t *testing.T) {
	// out_len uses the full low 32 bits.
	got := packResult(errOK, 0xFFFFFFFF)
	if gotErr := uint32(got >> 32); gotErr != errOK {
		t.Errorf("high bits: got %d, want %d", gotErr, errOK)
	}
	if gotLen := uint32(got & 0xFFFFFFFF); gotLen != 0xFFFFFFFF {
		t.Errorf("low bits: got %d, want max uint32", gotLen)
	}
}

func TestClassifyFSError(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want uint32
	}{
		{"revoked", handle.ErrRevoked, errRevoked},
		{"out of scope", handle.ErrOutOfScope, errOutOfScope},
		{"unknown", errors.New("whatever"), errIO},
		{"not found via os", os.ErrNotExist, errIO},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyFSError(c.in); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

// TestBuildHostModule_ReadOnlyScopeExportsOnlyRead asserts the §3.5
// "only linked if authorised" invariant: a handle with reads but no
// writes produces an alf host module with alf_fs_read exported and
// alf_fs_write absent.
func TestBuildHostModule_ReadOnlyScopeExportsOnlyRead(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	baseDir := t.TempDir()
	fs := handle.NewFSHandle(capability.ID("test"), baseDir, handle.FSScope{
		Reads: []string{"data/"},
	})
	// Re-parent lifecycleCtx by wrapping in an Instance — NewFSHandle
	// alone leaves lifecycleCtx nil; the forge does the attachment.
	inst := handle.NewInstance(context.Background(), capability.ID("test"), handle.Grants{FS: fs})
	t.Cleanup(inst.Close)

	mod, err := BuildHostModule(context.Background(), e.Runtime(), inst.FS)
	if err != nil {
		t.Fatalf("BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	defs := mod.ExportedFunctionDefinitions()
	if _, ok := defs[fnAlfFSRead]; !ok {
		t.Errorf("%s not exported despite declared reads", fnAlfFSRead)
	}
	if _, ok := defs[fnAlfFSWrite]; ok {
		t.Errorf("%s exported despite no declared writes", fnAlfFSWrite)
	}
}

func TestBuildHostModule_WriteOnlyScopeExportsOnlyWrite(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	baseDir := t.TempDir()
	fs := handle.NewFSHandle(capability.ID("test"), baseDir, handle.FSScope{
		Writes: []string{"out/"},
	})
	inst := handle.NewInstance(context.Background(), capability.ID("test"), handle.Grants{FS: fs})
	t.Cleanup(inst.Close)

	mod, err := BuildHostModule(context.Background(), e.Runtime(), inst.FS)
	if err != nil {
		t.Fatalf("BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	defs := mod.ExportedFunctionDefinitions()
	if _, ok := defs[fnAlfFSRead]; ok {
		t.Errorf("%s exported despite no declared reads", fnAlfFSRead)
	}
	if _, ok := defs[fnAlfFSWrite]; !ok {
		t.Errorf("%s not exported despite declared writes", fnAlfFSWrite)
	}
}

func TestBuildHostModule_BothExported(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	baseDir := t.TempDir()
	fs := handle.NewFSHandle(capability.ID("test"), baseDir, handle.FSScope{
		Reads:  []string{"data/"},
		Writes: []string{"out/"},
	})
	inst := handle.NewInstance(context.Background(), capability.ID("test"), handle.Grants{FS: fs})
	t.Cleanup(inst.Close)

	mod, err := BuildHostModule(context.Background(), e.Runtime(), inst.FS)
	if err != nil {
		t.Fatalf("BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	defs := mod.ExportedFunctionDefinitions()
	if len(defs) != 2 {
		t.Errorf("want 2 exports, got %d: %v", len(defs), keys(defs))
	}
}

func TestBuildHostModule_NilFSProducesEmptyModule(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	mod, err := BuildHostModule(context.Background(), e.Runtime(), nil)
	if err != nil {
		t.Fatalf("BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	if got := len(mod.ExportedFunctionDefinitions()); got != 0 {
		t.Errorf("want 0 exports, got %d", got)
	}
}

// TestBuildHostModule_SameRuntimeTwiceFails ensures we can't accidentally
// register two "alf" host modules on the same runtime — wazero rejects
// duplicate module names, which is the correct behaviour for our
// one-guest-one-host-module model.
func TestBuildHostModule_SameRuntimeTwiceFails(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	baseDir := t.TempDir()
	fs := handle.NewFSHandle(capability.ID("test"), baseDir, handle.FSScope{
		Reads: []string{"data/"},
	})
	inst := handle.NewInstance(context.Background(), capability.ID("test"), handle.Grants{FS: fs})
	t.Cleanup(inst.Close)

	mod1, err := BuildHostModule(context.Background(), e.Runtime(), inst.FS)
	if err != nil {
		t.Fatalf("first BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod1.Close(context.Background()) })

	_, err = BuildHostModule(context.Background(), e.Runtime(), inst.FS)
	if err == nil {
		t.Fatal("second BuildHostModule on same runtime: want error, got nil")
	}
}

// keys is a small helper for diagnostic output.
func keys(m map[string]api.FunctionDefinition) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// classifyFSError_PathEnsureFileReadable is a sanity check that the
// FSHandle layer + classifier produce the codes WASM.md §3.3 promises
// for the common paths: file exists + in scope → errOK; file missing
// → errIO.
func TestClassifyFSError_PathScenarios(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "present.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := handle.NewFSHandle(capability.ID("test"), baseDir, handle.FSScope{
		Reads: []string{"present.txt", "other.txt"},
	})
	inst := handle.NewInstance(context.Background(), capability.ID("test"), handle.Grants{FS: fs})
	t.Cleanup(inst.Close)

	if _, err := inst.FS.Read(context.Background(), "present.txt"); err != nil {
		t.Errorf("read present: unexpected err %v", err)
	}
	_, err := inst.FS.Read(context.Background(), "other.txt")
	if err == nil {
		t.Fatal("read missing file: want error, got nil")
	}
	if got := classifyFSError(err); got != errIO {
		t.Errorf("missing file classifies to %d, want errIO", got)
	}
}
