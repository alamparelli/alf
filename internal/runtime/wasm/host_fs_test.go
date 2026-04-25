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
// TestBuildHostModule_AlwaysExportsBothFunctions: under the shared
// host-module model (one "alf" per runtime, per-guest dispatch via
// mod.Name()), exports are unconditional. The structural gate against
// undeclared imports is CheckImports — see TestCheckImports_* — which
// runs before InstantiateModule and rejects guests whose import list
// is not a subset of their manifest's declared scope.
func TestBuildHostModule_AlwaysExportsBothFunctions(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	reg := newHostFSRegistry()
	mod, err := BuildHostModule(context.Background(), e.Runtime(), reg)
	if err != nil {
		t.Fatalf("BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(context.Background()) })

	defs := mod.ExportedFunctionDefinitions()
	if _, ok := defs[fnAlfFSRead]; !ok {
		t.Errorf("%s not exported", fnAlfFSRead)
	}
	if _, ok := defs[fnAlfFSWrite]; !ok {
		t.Errorf("%s not exported", fnAlfFSWrite)
	}
	if got := len(defs); got != 2 {
		t.Errorf("want 2 exports, got %d: %v", got, keys(defs))
	}
}

// TestBuildHostModule_NilRegistryRejected covers the guard: callers
// must always provide the per-runtime hostFSRegistry. Passing nil
// would mean every guest's alf_fs_* call has nowhere to look up its
// FSHandle — a footgun.
func TestBuildHostModule_NilRegistryRejected(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	if _, err := BuildHostModule(context.Background(), e.Runtime(), nil); err == nil {
		t.Fatal("nil registry must be rejected")
	}
}

// TestBuildHostModule_SameRuntimeTwiceFails: wazero rejects duplicate
// module names. NewRuntime registers the host module exactly once at
// construction; this test ensures the underlying mechanism would still
// catch a second registration if a future change accidentally re-called
// BuildHostModule on the same runtime.
func TestBuildHostModule_SameRuntimeTwiceFails(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	reg := newHostFSRegistry()
	mod1, err := BuildHostModule(context.Background(), e.Runtime(), reg)
	if err != nil {
		t.Fatalf("first BuildHostModule: %v", err)
	}
	t.Cleanup(func() { _ = mod1.Close(context.Background()) })

	_, err = BuildHostModule(context.Background(), e.Runtime(), reg)
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
