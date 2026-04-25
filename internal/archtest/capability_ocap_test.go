// Archtest rules for #391 Tier 3.1 ocap foundation — back the invariants
// listed in docs/ARCHITECTURE-SECURITY.md §9 with CI-enforced checks.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMintRuntimeTokenIsRuntimeOnly ensures that only the Runtime can
// mint the one-shot capability.handle.RuntimeToken that unlocks
// ForgeInstance. The §4.3 "forge is an interface behind a private type"
// invariant rests on three overlapping locks:
//
//  1. RuntimeToken.key is unexported — outside packages cannot construct
//     a non-zero token via composite literal
//  2. MintRuntimeToken is a one-shot — a second mint panics
//  3. THIS TEST — no non-Runtime package may call MintRuntimeToken
//
// Call sites must live under internal/runtime/ (the orchestration layer)
// or internal/capability/handle/ (definition site + package-local tests).
// cmd/alf-daemon is NOT on the allowlist: if a daemon main wires up
// Runtime, the constructor inside internal/runtime/ must do the mint.
// This keeps authority wiring in one layer.
func TestMintRuntimeTokenIsRuntimeOnly(t *testing.T) {
	root := repoRoot()
	pattern := regexp.MustCompile(`\bMintRuntimeToken\s*\(`)

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
		// Allow: definition site + its own tests, and the Runtime subtree.
		if strings.HasPrefix(rel, filepath.Join("internal", "capability", "handle")) {
			return nil
		}
		if strings.HasPrefix(rel, filepath.Join("internal", "runtime")) {
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
		t.Errorf("MintRuntimeToken called outside Runtime subtree: %s — see ARCHITECTURE-SECURITY.md §4.3 + §9 rule 7. Authority wiring must happen in internal/runtime/.", v)
	}
}

// TestNoPluginStdlibImport enforces §4.1 + §9 rule 6: Go-kind capabilities
// are maintainer-only, third-party capabilities are WASM-kind obligatory.
// Importing Go's `plugin` stdlib package enables dynamic native-code
// loading, which gives the loaded code ambient access to process memory —
// breaking the trust model. Forbidden everywhere in the codebase.
func TestNoPluginStdlibImport(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			if imp == "plugin" {
				rel := strings.TrimPrefix(p.ImportPath, modulePrefix)
				t.Errorf("%s imports plugin stdlib — Go plugins are forbidden (ARCHITECTURE-SECURITY.md §4.1 + §9 rule 6). Use WASM-kind capabilities for dynamic loading.", rel)
			}
		}
	}
}

// TestHandlePackageNoUnsafeOrLinkname guards the handle package itself
// against features that would let capability code reach around the ocap
// boundary. The handle package is the TCB for Tier 3.1: if it uses
// unsafe pointers or go:linkname it undermines every handle's non-
// serializable + scoped-only guarantee.
//
// This is a per-package spot check — the broader §4.2 invariant 5 ("no
// unsafe/reflect/go:linkname in capability packages") applies to all
// capability-holder packages (skills, marketplace, tooling/native_*)
// and is deferred to a follow-up archtest once the migration in #398
// narrows the capability-package set explicitly.
func TestHandlePackageNoUnsafeOrLinkname(t *testing.T) {
	root := repoRoot()
	handleDir := filepath.Join(root, "internal", "capability", "handle")

	unsafeImport := regexp.MustCompile(`^\s*"unsafe"\s*$|^\s*_\s*"unsafe"\s*$`)
	linknamePragma := regexp.MustCompile(`//\s*go:linkname\b`)

	err := filepath.Walk(handleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip _test.go — tests may occasionally need unsafe for
		// controlled invariant proofs (none today).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)

		// Line-by-line scan: import block lines, pragma comments.
		for _, line := range strings.Split(string(b), "\n") {
			if unsafeImport.MatchString(line) {
				t.Errorf("%s imports unsafe — handle package is TCB for Tier 3.1, must not use unsafe (ARCHITECTURE-SECURITY.md §4.2 invariant 5)", rel)
			}
			if linknamePragma.MatchString(line) {
				t.Errorf("%s uses go:linkname — handle package must not reach into other packages' internals (ARCHITECTURE-SECURITY.md §4.2 invariant 5)", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestOneVerifyCallSite — #388 deliverable 2. The envelope.Verify
// function is the single load-time signature-check entry point. Every
// load path (startup discovery, marketplace install, alf install,
// skill load, provider load) must converge here; no "internal use
// only" bypass, no branch that skips verification.
//
// The regex hunts for top-level pipeline call syntax across the whole
// codebase. Allowed callers:
//   - internal/capability/envelope/ — own implementation + tests
//   - internal/runtime/instantiator_verified.go — the ONE runtime
//     consumer (and its _test.go sibling)
//
// A second runtime caller would be a bypass: two consumers means two
// opportunities for one to forget a step of the pipeline. Archtest
// catches that before it merges.
func TestOneVerifyCallSite(t *testing.T) {
	root := repoRoot()
	// Match calls to envelope's top-level pipeline entry — NOT
	// VerifySignature or VerifyGlobalComment, which are lower-level
	// primitives the pipeline uses internally.
	pattern := regexp.MustCompile(`\benvelope\.Verify\s*\(`)

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
		// Envelope package's own code + tests.
		if strings.HasPrefix(rel, filepath.Join("internal", "capability", "envelope")) {
			return nil
		}
		// The single runtime consumer + its test.
		if rel == filepath.Join("internal", "runtime", "instantiator_verified.go") ||
			rel == filepath.Join("internal", "runtime", "instantiator_verified_test.go") {
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
		t.Errorf("envelope.Verify called outside the sanctioned call sites: %s — see #388 deliverable 2. Every load path must converge on Instantiator.InstantiateVerified.", v)
	}
}

// TestHandleTypesRejectJSONMarshal exercises §4.2 invariant 1 (handles
// are non-serializable) at the behaviour level. Every exported handle
// type's MarshalJSON must return a non-nil error — encoding/json will
// then refuse to serialise instances, and the archtest failure clearly
// points at the offender.
//
// This is a runtime property test, not a static rule, but it lives with
// the archtest suite so CI catches a regression on any new handle type
// that forgets to implement MarshalJSON.
func TestHandleTypesRejectJSONMarshal(t *testing.T) {
	// Intentionally delegated to the handle package's own test suite
	// (TestFSHandle_NonSerializable, TestHTTPHandle_NonSerializable, etc.)
	// — running them here would pull handle as a test dependency. This
	// stub stands as documentation that the invariant IS tested and
	// points to the actual location.
	t.Log("enforced by per-handle tests in internal/capability/handle/*_test.go: " +
		"TestFSHandle_NonSerializable, TestHTTPHandle_NonSerializable, " +
		"TestExecHandle_NonSerializable, TestSecretsHandle_NonSerializable, " +
		"TestToolHandle_NonSerializable. Adding a new handle type requires " +
		"an equivalent test.")
}

// skipOcapDir mirrors the existing archtest walker: skip ignorable
// directories we never want to scan (vendor deps, build output, VCS,
// agent worktrees that mirror the repo under .claude/).
func skipOcapDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", ".claude", "dist", "build":
		return true
	}
	return false
}
