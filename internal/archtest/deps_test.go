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

// TestDependencyRules verifies the v0.7.10 dependency rules.
//
// INFORMATIONAL: violations are reported via t.Log. The test never fails.
// Flip to t.Errorf once the migration baseline is clean (end of Step 4 / start
// of Step 5).
func TestDependencyRules(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	foundationViolations := 0
	consumerViolations := 0

	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePrefix)

		// Rule A: foundation packages must not import forbidden peers.
		if forbidden, ok := forbiddenImports[rel]; ok {
			for _, imp := range p.Imports {
				impRel := strings.TrimPrefix(imp, modulePrefix)
				if _, bad := forbidden[impRel]; bad {
					t.Logf("VIOLATION (foundation cross-import): %s imports %s", rel, impRel)
					foundationViolations++
				}
			}
			continue
		}

		// Rule B: consumers (everything else under internal/, excluding runtime)
		// must not directly import any leaf foundation package.
		if !strings.HasPrefix(rel, "internal/") {
			continue
		}
		if _, isFoundation := foundation[rel]; isFoundation {
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

	t.Logf("archtest summary: foundationViolations=%d consumerViolations=%d pkgsScanned=%d",
		foundationViolations, consumerViolations, len(pkgs))
	t.Log("archtest is INFORMATIONAL at Step 0 — these violations are expected during migration")
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
