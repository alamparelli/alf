// Archtest rules for #386 — back the WASM layer invariants listed in
// docs/WASM.md §3.4 + §3.5 + §4.1 with CI-enforced checks.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestWazeroImportConfinedToWASMPackage enforces that only
// internal/runtime/wasm/ reaches the wazero runtime directly. A
// second importer would mean a second place that could decide what
// host imports get linked into a module, undermining the "forge is
// the only place that wires authority" invariant (WASM.md §3.5).
//
// Exception: the compile-only API (internal/runtime/wasm/builder)
// is a pure Go toolchain wrapper and does not import wazero.
func TestWazeroImportConfinedToWASMPackage(t *testing.T) {
	root := repoRoot()
	allowed := filepath.Join("internal", "runtime", "wasm")

	// Matches `import "github.com/tetratelabs/wazero..."` forms:
	// single-line imports, block-imports, with or without alias.
	pat := regexp.MustCompile(`"github\.com/tetratelabs/wazero(?:/[^"]*)?"`)

	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipOcapDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, allowed) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pat.Match(b) {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, v := range violations {
		t.Errorf("wazero imported outside internal/runtime/wasm: %s — only the forge package may touch the wazero runtime (WASM.md §3.5).", v)
	}
}

// TestWASMHostFSUsesMemoryReadWriteOnly enforces docs/WASM.md §3.4
// hard rule + 0.7.9 audit finding C1: host-function code in
// internal/runtime/wasm/host_fs.go must access guest memory ONLY
// via api.Memory.Read and api.Memory.Write. Raw pointer arithmetic
// from guest-supplied offsets is a confused-deputy trap — the
// bounds check in api.Memory is the single invariant that prevents
// guest offsets from reaching host memory.
//
// Forbidden constructs in host_fs.go:
//   - `unsafe.Pointer` / `unsafe.Slice` / `unsafe.Add`
//   - `reflect.SliceHeader` / `reflect.StringHeader` (legacy raw
//     pointer access)
//   - `go:linkname`
//
// Test scope: internal/runtime/wasm/host_fs.go only (the host-ABI
// surface). Other files in internal/runtime/wasm may legitimately
// use unsafe for private guest-memory experiments guarded by tests.
func TestWASMHostFSUsesMemoryReadWriteOnly(t *testing.T) {
	root := repoRoot()
	target := filepath.Join(root, "internal", "runtime", "wasm", "host_fs.go")

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}

	forbidden := []struct {
		re   *regexp.Regexp
		name string
	}{
		{regexp.MustCompile(`"unsafe"`), "unsafe stdlib import"},
		{regexp.MustCompile(`\bunsafe\.`), "unsafe package usage"},
		{regexp.MustCompile(`\breflect\.SliceHeader\b`), "reflect.SliceHeader"},
		{regexp.MustCompile(`\breflect\.StringHeader\b`), "reflect.StringHeader"},
		{regexp.MustCompile(`//\s*go:linkname\b`), "go:linkname pragma"},
	}

	for _, f := range forbidden {
		if f.re.Match(b) {
			t.Errorf("host_fs.go uses %s — host functions must access guest memory only via api.Memory.Read/Write (WASM.md §3.4 + 0.7.9 audit C1).", f.name)
		}
	}
}

// TestWASMPackageNoUnsafeOrLinkname enforces §4.2 invariant 5 on
// the WASM subtree as a whole (except _test.go files — tests may
// need controlled invariant proofs). Mirrors the same rule applied
// to internal/capability/handle by TestHandlePackageNoUnsafeOrLinkname.
//
// Exception: skills.d/wasm/*/src/main.go are GUEST code. Guests
// compile to wasip1 and legitimately need unsafe to get buffer
// pointers from Go slices — that's the whole point of the ABI. The
// //go:build wasip1 tag isolates them from host builds; this
// archtest scopes to the HOST side of the layer.
func TestWASMPackageNoUnsafeOrLinkname(t *testing.T) {
	root := repoRoot()
	wasmDir := filepath.Join(root, "internal", "runtime", "wasm")

	unsafeImport := regexp.MustCompile(`^\s*"unsafe"\s*$|^\s*_\s*"unsafe"\s*$`)
	linknamePragma := regexp.MustCompile(`//\s*go:linkname\b`)

	err := filepath.Walk(wasmDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, line := range strings.Split(string(b), "\n") {
			if unsafeImport.MatchString(line) {
				t.Errorf("%s imports unsafe — internal/runtime/wasm host code must not use unsafe (WASM.md §3.4).", rel)
			}
			if linknamePragma.MatchString(line) {
				t.Errorf("%s uses go:linkname — WASM host layer must not reach into other packages' internals.", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
