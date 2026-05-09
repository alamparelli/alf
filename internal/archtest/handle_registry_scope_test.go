// Archtest rules for #392 Stage 2 — pin the caller scope of the
// HandleRegistry mutating surface. The runtime token is the
// load-bearing gate (ErrInvalidRegistryToken at runtime); these tests
// add compile-time defence in depth.
//
// Without this archtest, a future refactor that exposes the
// Instantiator's token (e.g. to share with a different subsystem)
// would silently widen the registry-mutation surface to anyone
// holding the token — and the only check would be the token itself.
// Pinning the importers + the call sites means the boundary is
// auditable from `git grep` alone.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// allowedRegistryImporters lists every directory prefix that may
// import the *handle.HandleRegistry type (via NewHandleRegistry, or
// via a handle.HandleRegistry parameter). These are:
//
//   - internal/capability/handle/ — definition + package-local tests
//   - internal/runtime/ — Instantiator.SeedHandleRegistry plumbs the
//     runtime token to RegisterCore; Stage 3 will add a sibling for
//     provider exports.
//   - cmd/alf-daemon/ — daemon boot constructs the registry and
//     passes it to Instantiator.SeedHandleRegistry. Read-only access
//     (Lookup, List, Len) is appropriate here.
//   - internal/archtest/ — this file itself.
//
// Any other package that wants to read the registry should accept it
// as a narrow read-only interface (not the full struct), so adding a
// reader doesn't widen the import scope.
var allowedRegistryImporters = []string{
	filepath.Join("internal", "capability", "handle"),
	filepath.Join("internal", "runtime"),
	filepath.Join("cmd", "alf-daemon"),
	filepath.Join("internal", "archtest"),
}

// TestNewHandleRegistryImportScopePinned ensures the constructor
// `handle.NewHandleRegistry()` is only invoked from packages on
// allowedRegistryImporters. Adding a caller from a new directory
// requires extending the allowlist with a one-line justification —
// the same pattern as `TestExecutorImportScopePinned`.
func TestNewHandleRegistryImportScopePinned(t *testing.T) {
	root := repoRoot()
	pattern := regexp.MustCompile(`\bhandle\.NewHandleRegistry\s*\(`)

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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if isAllowedRegistryCaller(rel) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pattern.Match(b) {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, v := range violations {
		t.Errorf("handle.NewHandleRegistry called outside the allowed scope: %s — "+
			"#392 Stage 2 invariant. Either update allowedRegistryImporters with "+
			"justification, or accept a narrow read-only interface instead.", v)
	}
}

// TestRegisterCoreCallerScopePinned ensures the registry's mutating
// `RegisterCore` method is only called from allowed packages. This
// is belt-and-braces alongside the runtime-token check inside
// HandleRegistry.Register: even if a future refactor accidentally
// exposes the token, the import-time boundary still holds.
//
// `RegisterCore` is a unique-name method (no other Type.RegisterCore
// in the codebase as of Stage 2), so the regex is unambiguous.
func TestRegisterCoreCallerScopePinned(t *testing.T) {
	root := repoRoot()
	pattern := regexp.MustCompile(`\.RegisterCore\s*\(`)

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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if isAllowedRegistryCaller(rel) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pattern.Match(b) {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, v := range violations {
		t.Errorf(".RegisterCore called outside the allowed scope: %s — "+
			"#392 Stage 2 invariant. RegisterCore mutates the runtime registry "+
			"and must be gated by the §4.3 runtime token.", v)
	}
}

func isAllowedRegistryCaller(relPath string) bool {
	dir := filepath.Dir(relPath)
	for _, prefix := range allowedRegistryImporters {
		if dir == prefix || strings.HasPrefix(dir, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
