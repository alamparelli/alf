package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hardcodedModelPattern matches claude-haiku-*, claude-sonnet-*, claude-opus-*
// string literals embedded in Go source (source-level, not regex on bytes).
// Deliberately anchored on the short prefixes we enforce — unrelated Anthropic
// or marketing references would not match.
var hardcodedModelPattern = regexp.MustCompile(`"claude-(haiku|sonnet|opus)-[0-9]`)

// TestHardcodedModelFallback reports non-test .go files outside internal/ai/
// that embed a hardcoded claude model identifier string. The single source of
// truth is ai.ResolveModel (see technical/ARCHITECTURE-v0.7.10.md §2.3 rule 1).
//
// INFORMATIONAL during milestone 0.7.9: violations logged via t.Logf, not
// t.Errorf. Flip to enforcing in #340 A5 once consumer fallbacks are purged.
//
// Exclusions:
//   - *_test.go files (fixtures frequently embed real model IDs).
//   - internal/ai/ and its sub-packages (canonical home).
//   - technical/ARCHITECTURE-v0.7.10.md and other non-Go files (not scanned).
func TestHardcodedModelFallback(t *testing.T) {
	root := repoRoot()
	var violations []string
	var scanned int

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "vendor" || base == ".git" || base == "node_modules" || base == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		// The canonical home and its sub-packages are allowed.
		if strings.HasPrefix(rel, "internal/ai/") || rel == "internal/ai" {
			return nil
		}
		// Test-infrastructure packages carry fixtures, not production fallbacks.
		if strings.Contains(rel, "memtest/") || strings.Contains(rel, "testutil/") {
			return nil
		}
		scanned++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := stripLineComment(line)
			if strings.TrimSpace(code) == "" {
				continue
			}
			if hardcodedModelPattern.MatchString(code) {
				violations = append(violations, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Skipf("walk failed: %v", err)
		return
	}

	for _, v := range violations {
		t.Logf("HARDCODED MODEL: %s", v)
	}
	t.Logf("archtest summary: hardcodedViolations=%d filesScanned=%d", len(violations), scanned)
	t.Log("hardcoded-model rule is INFORMATIONAL until #340 A5 lands consumer migration")
}

// stripLineComment returns line with anything starting at an unquoted "//"
// removed. It is naive about // inside strings, but claude-* model IDs never
// contain // so any "//" ahead of a model literal is a Go comment in practice.
func stripLineComment(line string) string {
	inStr := false
	for i := 0; i < len(line)-1; i++ {
		switch line[i] {
		case '"':
			if i == 0 || line[i-1] != '\\' {
				inStr = !inStr
			}
		case '/':
			if !inStr && line[i+1] == '/' {
				return line[:i]
			}
		}
	}
	return line
}

// itoa avoids pulling strconv into the test file for a single use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
