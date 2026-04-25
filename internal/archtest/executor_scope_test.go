// Archtest rule for #383 (reframed) — pin the current allowed set of
// `tooling.Executor` importers so the import graph cannot drift outward
// without explicit review.
//
// Background: the original #383 goal — "make Executor package-private
// so Sandbox.Apply cannot be bypassed" — assumed Sandbox.Apply enforced
// authority via Policy-on-ctx. After #406 + #391 + #386, that surface
// was redesigned: handles carry authority, Apply only stashes Identity
// for audit (§4.4 of ARCHITECTURE-SECURITY.md). The "bypass = security
// hole" framing no longer applies for Go-kind tools (TCB, §4.1).
//
// What still has value: keeping the import graph for `tooling.Executor`
// scoped to wiring + runtime + provider layers. This archtest freezes
// that scope. Adding a new package to the allow-list requires updating
// this test AND justifying why the new caller cannot route through an
// existing wiring layer.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// allowedExecutorImporters is the curated set of files that may
// reference `tooling.Executor` (the type) directly. Adding a new entry
// is a deliberate change — review the §4.4 + §9 rationale before
// touching this list.
var allowedExecutorImporters = []string{
	// Daemon wiring layer — instantiates the singleton Executor.
	filepath.Join("cmd", "alf-daemon", "main.go"),

	// Provider tool-loop adapter — bridges tooling.Executor to the
	// provider.ToolExecutor interface used by ai/provider/toolloop.go.
	filepath.Join("internal", "ai", "provider", "tooling_adapter.go"),

	// CC chat service — embeds Executor for the in-CC tool dispatch
	// path. Will move under runtime/ when #389 (Skills as ocap) lands
	// and tool dispatch unifies.
	filepath.Join("internal", "controlcenter", "chat_service.go"),

	// Runtime owns the unified comms / agents / tooling pipeline.
	filepath.Join("internal", "runtime", "agents", "orchestrator.go"),
	filepath.Join("internal", "runtime", "engine.go"),
	filepath.Join("internal", "runtime", "pipeline.go"),
}

// TestExecutorImportScopePinned enforces that `tooling.Executor` (the
// type) is referenced only from the curated allow-list above. A new
// importer outside that list is a CI failure — the right response is
// either (a) route through an existing wiring layer or (b) update the
// allow-list with explicit justification + reviewer sign-off.
//
// This is the post-reframe replacement for the doc-claimed-but-never-
// implemented `TestExecutorImplPrivate`. Same intent (one-seam scoping
// for tool execution) calibrated to what is actually enforceable today
// without the larger Executor-unification refactor (deferred to a 0.9.0
// follow-up — see #383 close-out comment).
func TestExecutorImportScopePinned(t *testing.T) {
	root := repoRoot()

	allowed := make(map[string]bool, len(allowedExecutorImporters))
	for _, p := range allowedExecutorImporters {
		allowed[p] = true
	}

	// Match `tooling.Executor` as a type reference (not in comments).
	// We accept either the package-qualified form (`tooling.Executor`)
	// or the pointer form (`*tooling.Executor`). Comment lines are
	// skipped so historical references in docstrings don't trip the rule.
	pat := regexp.MustCompile(`\*?tooling\.Executor\b`)

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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		// Files inside internal/tooling/ legitimately reference Executor
		// — that's its home package.
		if strings.HasPrefix(rel, filepath.Join("internal", "tooling")+string(filepath.Separator)) {
			return nil
		}
		// Files inside internal/sandbox/integrity/ have a doc comment
		// referencing the executor — handled by the comment-skip below.
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if pat.MatchString(line) && !allowed[rel] {
				violations = append(violations, formatViolation(rel, i+1, line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, v := range violations {
		t.Errorf("tooling.Executor referenced from a non-allow-listed file: %s\n"+
			"The current allow-list is in archtest/executor_scope_test.go (allowedExecutorImporters).\n"+
			"To add a new importer: route through an existing wiring layer if possible; otherwise\n"+
			"update the allow-list with §4.4-aligned justification and reviewer sign-off.", v)
	}
}
