// Archtest rule for #420 + ARCHITECTURE-SECURITY.md §4.1 — pin the
// "no dynamic Go plugins, ever" invariant. The Go stdlib `plugin`
// package would let arbitrary .so files inject code into the daemon
// process with full ambient authority, bypassing every guard in the
// 3-layer model (no wazero wall, no envelope signature on the .so,
// no Tier 3.1 forge handle). The doctrine forbids this path
// categorically; this archtest pins the forbiddance.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoGoPluginImport enforces the §4.1 ban on dynamic Go plugins.
// Matches any import of the stdlib `plugin` package — single-line,
// block-form, with or without alias.
func TestNoGoPluginImport(t *testing.T) {
	root := repoRoot()

	// Imports of "plugin" (stdlib). Excludes any namespaced third-party
	// path that happens to end in /plugin (e.g. some Vault auth modules)
	// — only the bare stdlib name is the dynamic-loader hazard.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^\s*"plugin"\s*$`),                  // bare line inside import block
		regexp.MustCompile(`^\s*\w+\s+"plugin"\s*$`),            // aliased import inside block
		regexp.MustCompile(`^\s*import\s+"plugin"\s*$`),          // single-line import
		regexp.MustCompile(`^\s*import\s+\w+\s+"plugin"\s*$`),    // single-line aliased
	}

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
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, pat := range patterns {
				if pat.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					violations = append(violations, formatViolation(rel, i+1, line))
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, v := range violations {
		t.Errorf("forbidden import of stdlib `plugin`: %s\n"+
			"Per ARCHITECTURE-SECURITY.md §4.1 + #420, dynamic Go plugins are banned. "+
			"All third-party / LLM-authored code must be WASM-kind. Use internal/runtime/wasm/ "+
			"loader or the wasm-builder skill.", v)
	}
}
