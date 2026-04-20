// Package archtest hosts architectural tests that enforce the v0.7.10
// dependency rules defined in technical/ARCHITECTURE-v0.7.10.md.
//
// At Step 0 (#334) this test is INFORMATIONAL: it logs violations via t.Log
// and never calls t.Fatal / t.Errorf. It becomes enforcing once migration
// (Steps 1 → 4) completes and the baseline is clean.
package archtest_test

import (
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const modulePrefix = "github.com/alamparelli/alf/"

// The 5 foundational blocks.
var foundation = map[string]struct{}{
	"internal/capability": {},
	"internal/memory":     {},
	"internal/ai":         {},
	"internal/sandbox":    {},
	"internal/runtime":    {},
}

// For each foundation package, list the OTHER foundation packages it must not
// import. Runtime is allowed to import the four leaves; the leaves must not
// import each other nor runtime.
var forbiddenImports = map[string]map[string]struct{}{
	"internal/capability": {
		"internal/memory": {}, "internal/ai": {}, "internal/sandbox": {}, "internal/runtime": {},
	},
	"internal/memory": {
		"internal/capability": {}, "internal/ai": {}, "internal/sandbox": {}, "internal/runtime": {},
	},
	"internal/ai": {
		"internal/capability": {}, "internal/memory": {}, "internal/sandbox": {}, "internal/runtime": {},
	},
	"internal/sandbox": {
		"internal/capability": {}, "internal/memory": {}, "internal/ai": {}, "internal/runtime": {},
	},
	// runtime has no forbidden foundation imports.
	"internal/runtime": {},
}

// leafFoundation is the set of foundation packages that consumers must NOT
// import directly. They should import runtime instead.
var leafFoundation = map[string]struct{}{
	"internal/capability": {},
	"internal/memory":     {},
	"internal/ai":         {},
	"internal/sandbox":    {},
}

type pkgInfo struct {
	ImportPath string
	Imports    []string
}

// TestFoundationDependencyRules enforces that the five foundation packages
// (capability, memory, ai, sandbox, runtime) do not cross-import each other
// beyond what ARCHITECTURE-v0.7.10 §4 allows (runtime may import the four
// leaves; no leaf may import another leaf or runtime).
//
// This test is ENFORCING — violations fail the build. The rule hardens the
// contract at the heart of the v0.7.10 rework: accidental coupling inside
// the five-block foundation is a regression.
func TestFoundationDependencyRules(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePrefix)
		forbidden, ok := forbiddenImports[rel]
		if !ok {
			continue
		}
		for _, imp := range p.Imports {
			impRel := strings.TrimPrefix(imp, modulePrefix)
			if _, bad := forbidden[impRel]; bad {
				t.Errorf("foundation cross-import: %s imports %s (see technical/ARCHITECTURE-v0.7.10.md §4)", rel, impRel)
			}
		}
	}
}

// TestConsumerDependencyRules reports cases where a consumer (any internal/
// package outside the five foundation blocks) imports a leaf foundation
// package directly. Target state: consumers go through internal/runtime.
//
// INFORMATIONAL during milestone 0.7.9: violations are reported via t.Log.
// Flip to t.Errorf at the end of Step 4 (#340) once Runtime is written and
// consumers have been migrated.
func TestConsumerDependencyRules(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	consumerViolations := 0
	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePrefix)
		if !strings.HasPrefix(rel, "internal/") {
			continue
		}
		if _, isFoundation := foundation[rel]; isFoundation {
			continue
		}
		// Skip foundation-package-scoped children (e.g. internal/memory/dedup)
		// — they are part of the memory block, not independent consumers.
		if isFoundationChild(rel) {
			continue
		}
		for _, imp := range p.Imports {
			impRel := strings.TrimPrefix(imp, modulePrefix)
			if _, leaf := leafFoundation[impRel]; leaf {
				t.Logf("VIOLATION (consumer direct leaf import): %s imports %s (should import internal/runtime)", rel, impRel)
				consumerViolations++
			}
		}
	}

	t.Logf("archtest summary: consumerViolations=%d pkgsScanned=%d", consumerViolations, len(pkgs))
	t.Log("consumer rule is INFORMATIONAL until Step 4 (#340) lands runtime")
}

// isFoundationChild returns true for packages nested inside a foundation
// block (e.g. internal/memory/dedup, internal/memory/socketsrv). Those are
// part of the block itself and may import their parent freely.
func isFoundationChild(rel string) bool {
	for f := range foundation {
		if strings.HasPrefix(rel, f+"/") {
			return true
		}
	}
	return false
}

// listPackages runs `go list -json ./...` and decodes the stream.
func listPackages(t *testing.T) ([]pkgInfo, error) {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = repoRoot()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = cmd.Wait() }()

	var out []pkgInfo
	dec := json.NewDecoder(stdout)
	for {
		var p pkgInfo
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// repoRoot walks upward from the current dir until it finds go.mod.
// Keeps the test runnable regardless of where `go test` is invoked from.
func repoRoot() string {
	cmd := exec.Command("go", "env", "GOMOD")
	b, err := cmd.Output()
	if err != nil {
		return "."
	}
	p := strings.TrimSpace(string(b))
	if p == "" || p == "/dev/null" {
		return "."
	}
	// strip trailing /go.mod
	return strings.TrimSuffix(p, "/go.mod")
}
