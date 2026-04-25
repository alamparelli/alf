// Archtest rules for #397 — pin the TOML parser used by the verify
// path per §7.10.6 of docs/ARCHITECTURE-SECURITY.md.
//
// Background: the entire signature model rests on canonicalizing the
// authored manifest into bytes the signer + verifier agree on. Two
// different TOML parsers can produce different parse trees for the
// same input (different default-omission rules, different inline-table
// promotion, different number representations). If the verifier and
// the signer use different parsers, signatures verify on one and not
// the other — the SAML / JWT-class parser-divergence failure mode.
//
// Defence: exactly one TOML parser exists in the verify path, pinned
// via go.mod and enforced by archtest. Adding a different parser is a
// CI-blocking change requiring explicit security review.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pinnedTOMLParser is the only TOML parser allowed anywhere in the
// codebase. Per §7.10.6, the constraint applies to "the verify path
// and any package it calls"; in practice every TOML use must go
// through this parser since envelope.Validate is the authoritative
// schema gate. Tightening to "everywhere" simplifies enforcement and
// closes the worry that a non-verify path could one day call into
// the verify code with a parsed-via-other-parser AST.
const pinnedTOMLParser = "github.com/pelletier/go-toml/v2"

// alternativeTOMLParsers is the list of well-known Go TOML parsers
// that must NOT be imported. New entries should be added when a
// competing parser becomes popular enough to be tempting; the regex
// matches the import path literally.
var alternativeTOMLParsers = []string{
	"github.com/BurntSushi/toml",
	"github.com/pelletier/go-toml\"",       // v1 (note the closing-quote anchor)
	"github.com/naoina/toml",
	"github.com/komkom/toml",
}

// alternativeStructuredFormats covers other formats that could
// plausibly be used to author manifests (and would defeat the
// canonicalization guarantee). Per §7.10.6: "guard against future
// author of a manifest.xml path".
var alternativeStructuredFormats = []string{
	"gopkg.in/yaml.v3",
	"gopkg.in/yaml.v2",
	"sigs.k8s.io/yaml",
}

// TestNoAlternativeTOMLParserImported scans the codebase for any
// import of a TOML parser other than the pinned pelletier/go-toml/v2.
// A new importer fails CI; the right response is to use the pinned
// parser instead, OR open a security-review ticket if a real reason
// to add a second parser exists.
//
// Test files (_test.go) are scanned too — the same pinning rule
// applies to test fixtures, otherwise a test could subtly disagree
// with the production canonicalizer and mask a real divergence.
func TestNoAlternativeTOMLParserImported(t *testing.T) {
	root := repoRoot()

	patterns := make([]*regexp.Regexp, 0, len(alternativeTOMLParsers)+len(alternativeStructuredFormats))
	for _, p := range alternativeTOMLParsers {
		// Match `"path"` and `_ "path"` import forms. The trailing
		// `"` is in the literal for the v1 anchor; the rest get
		// regex-escaped + a closing quote.
		patterns = append(patterns, importMatcher(p))
	}
	for _, p := range alternativeStructuredFormats {
		patterns = append(patterns, importMatcher(p))
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		// Skip this archtest file itself — it contains the forbidden
		// strings as data (the allow-list patterns) which would
		// false-positive without skipping.
		if rel == filepath.Join("internal", "archtest", "parser_pinning_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, re := range patterns {
				if re.MatchString(line) {
					violations = append(violations, formatViolation(rel, i+1, line))
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	for _, v := range violations {
		t.Errorf("§7.10.6 violation — alternative parser imported: %s\n"+
			"The verify path must parse manifests with exactly one parser to prevent\n"+
			"parser-divergence attacks. Use %q instead, or open a security review\n"+
			"ticket if a second parser is genuinely required.", v, pinnedTOMLParser)
	}
}

// TestPinnedTOMLParserIsActuallyUsed sanity-checks that the pinned
// parser is referenced somewhere in the codebase. A future refactor
// that accidentally removes all uses (e.g., switching to a custom
// parser without realising) should be visible — the verify path
// would then have NO parser at all.
func TestPinnedTOMLParserIsActuallyUsed(t *testing.T) {
	root := repoRoot()
	pinned := importMatcher(pinnedTOMLParser)

	var found bool
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return nil
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
		b, _ := os.ReadFile(path)
		if pinned.Match(b) {
			found = true
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if !found {
		t.Errorf("pinned TOML parser %q is not imported anywhere in production code; "+
			"verify path may have lost its parser", pinnedTOMLParser)
	}
}

// importMatcher returns a regex that matches an `import` line for the
// given import path. Handles both single-line and block-import forms;
// matches `"path"` and `_ "path"` and `alias "path"`.
func importMatcher(importPath string) *regexp.Regexp {
	// Strip a trailing literal quote if the caller embedded one (used
	// for v1-vs-v2 disambiguation on `pelletier/go-toml`).
	suffix := ""
	if strings.HasSuffix(importPath, `"`) {
		importPath = strings.TrimSuffix(importPath, `"`)
		suffix = `"`
	}
	escaped := regexp.QuoteMeta(importPath)
	if suffix == "" {
		// Match `"path"` followed by end-of-line or whitespace, so we
		// don't match `"path/something/v2"` when looking for `path`.
		return regexp.MustCompile(`"` + escaped + `"`)
	}
	// Caller wants the closing quote anchored — used for `pelletier/go-toml`
	// (v1) so we don't accidentally match `pelletier/go-toml/v2`.
	return regexp.MustCompile(`"` + escaped + `"`)
}
