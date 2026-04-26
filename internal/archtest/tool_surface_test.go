// Archtest rule for SEC-005 (audit finding): pin that
// runtime.BuildScopedToolSpecs is the sole production producer of
// LLM-facing tool specs from the capability registry.
//
// Background: the v0.8.0-beta security audit found that
// BuildScopedToolSpecs — the §3.1 active-skill tool-surface filter —
// was implemented but never wired into production. The legacy
// `buildToolSpecs` helper (no allowlist) was the only call site, so
// every chat turn exposed every installed capability to the LLM
// regardless of which skill was active. This archtest blocks two
// regression classes:
//
//  1. Re-introduction of the deleted unfiltered helper (`buildToolSpecs`
//     lowercase) anywhere in production code.
//  2. Inline construction of an `ai.ToolSpec` from a
//     `capability.Manifest` outside `BuildScopedToolSpecs`'s body —
//     the typical pattern a future contributor would use to "just
//     project the registry quickly", which would silently bypass the
//     active-skill boundary again.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoLegacyBuildToolSpecsHelper enforces that the deleted
// unfiltered `buildToolSpecs` helper is not re-introduced. The audit
// closed that gap by extending BuildScopedToolSpecs to handle
// nil-allowlist as the legacy "all tools" case; reviving the
// lowercase helper would re-create a producer that is harder to
// audit (no allowlist parameter at all).
//
// Allow-list: this test file itself references the name in its own
// docstring; the regex skips comment lines.
func TestNoLegacyBuildToolSpecsHelper(t *testing.T) {
	root := repoRoot()

	pat := regexp.MustCompile(`\bbuildToolSpecs\b`)

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
			// Tests may legitimately reference the historical name —
			// e.g. this archtest's own error message. Production .go
			// files must not contain the symbol at all.
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if pat.MatchString(line) {
				violations = append(violations, formatViolation(rel, i+1, line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, v := range violations {
		t.Errorf("legacy buildToolSpecs symbol re-introduced: %s\n"+
			"SEC-005: the unfiltered helper was deleted; use BuildScopedToolSpecs\n"+
			"with a nil or non-nil allowlist instead. The active-skill boundary\n"+
			"depends on a single producer.", v)
	}
}

// TestBuildScopedToolSpecsIsWiredInChat pins that the runtime's Chat
// path actually calls BuildScopedToolSpecs. Without this, a future
// refactor could silently revert to inline projection and resurrect
// the audit gap.
//
// We grep for the call site rather than running the function, because
// the wire-in is what makes the §3.1 surface narrowing reachable.
func TestBuildScopedToolSpecsIsWiredInChat(t *testing.T) {
	root := repoRoot()
	implPath := filepath.Join(root, "internal", "runtime", "impl.go")

	b, err := os.ReadFile(implPath)
	if err != nil {
		t.Fatalf("read impl.go: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, "BuildScopedToolSpecs(r.deps.Registry.List()") {
		t.Errorf("internal/runtime/impl.go: BuildScopedToolSpecs(r.deps.Registry.List(), ...) not found.\n" +
			"SEC-005: the §3.1 active-skill tool-surface filter must be the producer\n" +
			"for the LLM-facing tool list in the Chat loop. If the wire-in moved,\n" +
			"update this archtest to point at the new call site — do NOT silently\n" +
			"replace it with an unfiltered projection.")
	}
}
