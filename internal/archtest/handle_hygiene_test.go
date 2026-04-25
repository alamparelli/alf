// Archtest rules for #398 — pin the handle hygiene invariants from
// docs/ARCHITECTURE-SECURITY.md §4.2 across the codebase.
//
// Background: ocap (#391) depends on handles not leaking outside their
// intended scope. Without explicit hygiene invariants, Go's reflection
// and serialization features undermine ocap structurally. #391 + #386
// shipped the per-handle tests + the WASM cross-check + the forge
// token. This file pins the remaining two invariants:
//
//   1. No unsafe / reflect / go:linkname / plugin in capability-touching
//      packages (§4.2 invariant 5).
//   2. Every exported *Handle type in internal/capability/handle/
//      declares MarshalJSON — catches the "added a new handle type but
//      forgot to make it non-serializable" footgun (§4.2 invariant 1).
//
// Output sanitization (§4.2 invariant 2) is deferred to #411 — it
// requires Runtime.Invoke as the single tool-execution seam. Until
// then, the per-handle MarshalJSON refusal is sufficient because every
// LLM-visible output path JSON-marshals before sending.
package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// capabilityTouchingDirs is the set of subtrees subject to the
// "no reflection / no unsafe" rule (§4.2 invariant 5). Anything that
// implements or registers a capability.Capability — directly or via an
// adapter — falls under this rule. Adding a directory means adding a
// new capability surface; the rule applies by construction.
var capabilityTouchingDirs = []string{
	filepath.Join("internal", "capability"),
	filepath.Join("internal", "skills"),
	filepath.Join("internal", "marketplace"),
}

// capabilityTouchingFilePrefixes covers files that aren't a whole
// subtree but are still capability-touching (capability adapters
// inside packages whose other files are unrelated). Use a relative
// path prefix so individual files are matched.
var capabilityTouchingFilePrefixes = []string{
	filepath.Join("internal", "tooling", "native_"),       // native tool wrappers
	filepath.Join("internal", "tooling", "capability_"),    // adapter
	filepath.Join("internal", "scheduler", "capability"),   // scheduler adapter
}

// TestNoUnsafeInCapabilityCode enforces §4.2 invariant 5: no `unsafe`,
// no `reflect`, no `go:linkname`, no `plugin` import in code that
// implements or adapts a capability.Capability. The reasoning:
//
//   - `unsafe`: bypasses non-serializable / scope hygiene by raw
//     pointer arithmetic
//   - `reflect`: can read/flip unexported fields (e.g. `revoked`,
//     `lifecycleCtx`) and forge handles via deep-equal tricks
//   - `go:linkname`: calls unexported runtime functions, also a forge
//     vector
//   - `plugin`: dynamic Go plugin loading is structurally incompatible
//     with §4.1's "Go-kind = maintainer-only, build-pipeline-signed"
//
// Currently zero violations exist (verified on first run). The test
// freezes that state. Adding a new capability adapter that needs
// reflection is a §4.2 review point — either the adapter belongs in
// runtime/ (not capability code) or the design needs revisiting.
func TestNoUnsafeInCapabilityCode(t *testing.T) {
	root := repoRoot()

	// Forbidden patterns — match top-level declarations and pragmas.
	// These check imports as written in source (single-line or in a
	// block) and the linkname pragma form.
	forbidden := []struct {
		re   *regexp.Regexp
		name string
		why  string
	}{
		{
			re:   regexp.MustCompile(`(?m)^\s*"unsafe"\s*$|^\s*_\s*"unsafe"\s*$|^\s*import\s+"unsafe"\b`),
			name: `import "unsafe"`,
			why:  "raw pointer arithmetic bypasses handle scope + non-serializable invariants",
		},
		{
			re:   regexp.MustCompile(`(?m)^\s*"reflect"\s*$|^\s*_\s*"reflect"\s*$|^\s*import\s+"reflect"\b`),
			name: `import "reflect"`,
			why:  "reflect can read/flip unexported handle fields (revoked, lifecycleCtx)",
		},
		{
			re:   regexp.MustCompile(`(?m)^\s*"plugin"\s*$|^\s*_\s*"plugin"\s*$|^\s*import\s+"plugin"\b`),
			name: `import "plugin"`,
			why:  "dynamic Go plugins violate §4.1 (Go-kind = maintainer-only, build-pipeline-signed)",
		},
		{
			re:   regexp.MustCompile(`//\s*go:linkname\b`),
			name: "go:linkname pragma",
			why:  "linkname can reach into unexported runtime helpers, bypassing the forge",
		},
	}

	var violations []string
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipOcapDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !isCapabilityTouching(rel) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, f := range forbidden {
			if f.re.Match(b) {
				violations = append(violations, fmt.Sprintf("%s: %s — %s", rel, f.name, f.why))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}

	for _, v := range violations {
		t.Errorf("§4.2 invariant 5 violation: %s\n"+
			"Capability-touching code must not use unsafe / reflect / linkname / plugin.\n"+
			"If the import is essential, the file likely belongs under internal/runtime/ rather than capability code.", v)
	}
}

// isCapabilityTouching reports whether a relative path is subject to
// the §4.2 hygiene rules. Either the file is inside one of the
// curated directory subtrees, or its name has a curated prefix.
func isCapabilityTouching(rel string) bool {
	sep := string(filepath.Separator)
	for _, dir := range capabilityTouchingDirs {
		if strings.HasPrefix(rel, dir+sep) || rel == dir {
			return true
		}
	}
	for _, prefix := range capabilityTouchingFilePrefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// TestAllHandleTypesNonSerializable enforces that every exported
// *Handle type defined in internal/capability/handle/ declares a
// MarshalJSON method. This is a meta-test that catches "added a new
// handle type but forgot to make it non-serializable" — the
// per-handle TestXHandle_NonSerializable tests cover the runtime
// behaviour for known types, but they cannot detect a brand-new
// handle type silently shipping without the method.
//
// JSON is the only serialization path that matters in practice (LLM
// tool outputs are JSON; capability outputs are JSON via the unified
// pipeline). MarshalBinary / GobEncode are not enforced here —
// adding them is defense-in-depth that no current code path relies on,
// and adding them later does not require revisiting this test.
func TestAllHandleTypesNonSerializable(t *testing.T) {
	root := repoRoot()
	handleDir := filepath.Join(root, "internal", "capability", "handle")

	// Collect handle type declarations: `type XxxHandle struct {`.
	// We only care about exported (capitalised) types.
	typeDeclRe := regexp.MustCompile(`(?m)^type\s+([A-Z][A-Za-z0-9_]*Handle)\s+struct\s*\{`)
	// MarshalJSON method declarations on a *XxxHandle pointer receiver.
	marshalRe := regexp.MustCompile(`(?m)^func\s+\(\s*\w+\s+\*([A-Z][A-Za-z0-9_]*Handle)\s*\)\s+MarshalJSON\s*\(`)

	declared := map[string]string{} // type → file
	withMarshal := map[string]bool{}

	walkErr := filepath.Walk(handleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		for _, m := range typeDeclRe.FindAllStringSubmatch(src, -1) {
			declared[m[1]] = rel
		}
		for _, m := range marshalRe.FindAllStringSubmatch(src, -1) {
			withMarshal[m[1]] = true
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", handleDir, walkErr)
	}

	if len(declared) == 0 {
		t.Fatal("no *Handle types found in internal/capability/handle/ — refactor likely; update this test")
	}

	for typ, file := range declared {
		if !withMarshal[typ] {
			t.Errorf("§4.2 invariant 1 violation: %s declared in %s has no MarshalJSON method.\n"+
				"Every handle type must refuse JSON serialization (return ErrHandleNonSerializable).\n"+
				"Add `func (h *%s) MarshalJSON() ([]byte, error) { return nil, ErrHandleNonSerializable }`.", typ, file, typ)
		}
	}
}
