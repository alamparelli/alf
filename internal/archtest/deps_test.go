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

// consumerLeafExceptions whitelists legitimate direct leaf imports with a
// one-line justification. The rule "consumers import runtime, not leaves"
// is architecturally sound for *orchestration*, but the leaves also expose
// contract types (ai.ModelID, capability.Manifest, sandbox.PermissionSet,
// memory.Document) that consumers legitimately need to:
//
//   - Build runtime requests (scheduler → ai.ModelID for ConverseRequest)
//   - Implement foundation interfaces (scheduler.CommandCapability →
//     capability.Capability)
//   - Validate inputs against the foundation schema (marketplace →
//     sandbox.ValidatePermissions)
//   - Consume pure helpers (controlcenter → ai.ResolveModel)
//
// Every entry needs a short reason; adding a new one is a deliberate
// architectural decision and should be reviewed like any other PR change.
// See #340 R6.
var consumerLeafExceptions = map[string]map[string]string{
	"internal/scheduler": {
		"internal/ai":         "ai.ModelID/Strategy/ToolSpec to build runtime.ConverseRequest",
		"internal/capability": "scheduler.CommandCapability implements capability.Capability",
		"internal/memory":     "memory.CollectSchedulerPrompts — disk-read utility, not orchestration",
	},
	"internal/marketplace": {
		"internal/capability": "marketplace.appCapability adapter implements capability.Capability",
		"internal/sandbox":    "sandbox.ValidatePermissions / UntrustedMaxPermissions — schema validation",
	},
	"internal/tooling": {
		"internal/capability": "tooling.nativeCapability adapter implements capability.Capability",
	},
	"internal/skills": {
		"internal/capability": "skills.skillCapability adapter implements capability.Capability",
	},
	"internal/comms": {
		"internal/ai":     "ai.ModelID/Message/ToolSpec/MediaEntry to build runtime.ConverseRequest in runtime_invoke.go (#340 R4j3)",
		"internal/memory": "conversation history + pref store — state layer below Runtime orchestration",
	},
	"internal/controlcenter": {
		"internal/ai":     "ai.ModelID / ai.ResolveModel / ai.ToolSpec — contract types + pure helper",
		"internal/memory": "conversation + doc reads for UI rendering (handler_chat*, handler_memory*)",
	},
	"internal/memstore": {
		"internal/memory": "memstore extends the memory block (extractor / consolidator / embedder)",
	},
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

// TestConsumerDependencyRules enforces that consumers (any internal/
// package outside the five foundation blocks) reach foundation *behaviour*
// through internal/runtime, not through direct leaf imports.
//
// Direct leaf imports are allowed ONLY when listed in consumerLeafExceptions
// with a justification (contract types, interface implementation, pure
// helpers). Any unjustified direct leaf import fails the test.
//
// This test was INFORMATIONAL during milestone 0.7.9 (#334) while Steps 1–4
// migrated consumers onto Runtime. It flips to enforcing in #340 R6 after
// R4j landed and the remaining leaf imports were audited and classified.
func TestConsumerDependencyRules(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	// Track which exceptions are actually exercised. A stale exception
	// (consumer no longer imports a leaf) is a yellow flag — remove it so
	// the allowlist reflects reality.
	used := make(map[string]map[string]bool, len(consumerLeafExceptions))
	for consumer, exc := range consumerLeafExceptions {
		used[consumer] = make(map[string]bool, len(exc))
		for leaf := range exc {
			used[consumer][leaf] = false
		}
	}

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
			if _, leaf := leafFoundation[impRel]; !leaf {
				continue
			}
			// An exception on a consumer covers every sub-package: if
			// internal/comms is allowed to import internal/memory, then
			// internal/comms/sub is implicitly covered too (it would not
			// be called a separate consumer by the intent of the rule).
			consumerRoot := topLevelConsumer(rel)
			if justification, ok := consumerLeafExceptions[consumerRoot][impRel]; ok {
				used[consumerRoot][impRel] = true
				t.Logf("ALLOWED (justified): %s imports %s — %s", rel, impRel, justification)
				continue
			}
			t.Errorf("VIOLATION: %s imports %s — consumers must import internal/runtime instead. If this import is a legitimate contract/type/helper, add an entry to consumerLeafExceptions with justification.", rel, impRel)
		}
	}

	// Report unused exceptions. Not a hard failure (a package may be
	// temporarily deleted), but visible so reviewers can prune.
	for consumer, seen := range used {
		for leaf, wasSeen := range seen {
			if !wasSeen {
				t.Logf("STALE EXCEPTION (not exercised): %s → %s — consider removing from consumerLeafExceptions", consumer, leaf)
			}
		}
	}
}

// topLevelConsumer maps a nested consumer path to its top-level package. For
// example "internal/comms/sub" → "internal/comms". Exceptions are keyed on
// top-level consumers so internal re-structuring does not silently void an
// exception.
func topLevelConsumer(rel string) string {
	rest := strings.TrimPrefix(rel, "internal/")
	if i := strings.Index(rest, "/"); i >= 0 {
		return "internal/" + rest[:i]
	}
	return rel
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
