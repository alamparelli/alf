// Archtest rules for #392 Stage 1 — pin the WASI raw-imports
// classification + the reserved `alf:` namespace's core-handle
// allowlist. Both lists live in internal/capability/envelope/schema.go;
// these tests freeze the *content* of the lists against what
// MANIFEST-SCHEMA.md §3.4 + #392 spec mandate.
//
// Why archtest rather than unit test in the envelope package: the
// source of truth for these sets is the design doc, not the code.
// A future contributor that "tightens" the allowlist by removing a
// permitted import, or "loosens" the forbidden list, must update the
// doc AND this archtest in lockstep — if the spec moves, this test
// has to move; if the code moves silently, this test fires.
//
// The tests are intentionally implemented as readline scans of the
// envelope package source, not via importing the (unexported)
// `forbiddenRawImportModules` / `allowedRawImportModules` symbols.
// Importing them would let a contributor "fix" a failing archtest
// by mutating the slice value to match what the test expects;
// reading the source means the test fails LOUDLY against any
// drift in either direction.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// expectedForbiddenRawImports lists every prefix that MUST appear in
// the forbiddenRawImportModules slice. Entries follow the same order
// and exact string the §3.4 spec uses; a deviation in either direction
// (removal or rewording) fires.
var expectedForbiddenRawImports = []string{
	`"wasi:filesystem/"`,
	`"wasi:sockets/"`,
	`"wasi:random/random"`,
	`"wasi:io/streams"`,
}

// expectedAllowedRawImports lists every prefix that MUST appear in the
// allowedRawImportModules slice. Adding a new allowed import is a
// schema change requiring an update to MANIFEST-SCHEMA.md AND a
// corresponding entry here.
var expectedAllowedRawImports = []string{
	`"wasi:clocks/monotonic-clock"`,
	`"wasi:clocks/wall-clock"`,
	`"wasi:cli/environment"`,
	`"wasi:cli/exit"`,
	`"wasi:cli/stdin"`,
	`"wasi:cli/stdout"`,
	`"wasi:cli/stderr"`,
	`"wasi:cli/terminal-input"`,
	`"wasi:cli/terminal-output"`,
}

// expectedCoreHandleIDs lists every string that MUST appear as a key
// in the coreHandleIDs map. These are the handle kinds the daemon
// reserves under the `alf:` namespace; a [[depends]] declaration
// referencing `alf:<id>` for any other id is rejected with
// ErrDependsHandleNamespaceReserved.
var expectedCoreHandleIDs = []string{
	`"fs"`,
	`"http"`,
	`"exec"`,
	`"secrets"`,
	`"events.pub"`,
	`"events.sub"`,
	`"tool"`,
}

// TestRawImportsClassificationPinned is a #392 Stage 1 invariant: the
// WASI raw-imports allowlists in envelope/schema.go must match the
// spec verbatim. Removing a forbidden entry, removing an allowed
// entry, or reordering them are all flagged.
func TestRawImportsClassificationPinned(t *testing.T) {
	root := repoRoot()
	schemaPath := filepath.Join(root, "internal", "capability", "envelope", "schema.go")
	src, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", schemaPath, err)
	}

	forbidden := extractStringSlice(t, string(src), "forbiddenRawImportModules")
	allowed := extractStringSlice(t, string(src), "allowedRawImportModules")
	core := extractStringSlice(t, string(src), "coreHandleIDs")

	if !sliceEqualOrdered(forbidden, expectedForbiddenRawImports) {
		t.Errorf("forbiddenRawImportModules drift: got=%v, want=%v\n"+
			"Update MANIFEST-SCHEMA.md §3.4 + this archtest in lockstep, then the slice.",
			forbidden, expectedForbiddenRawImports)
	}
	if !sliceEqualOrdered(allowed, expectedAllowedRawImports) {
		t.Errorf("allowedRawImportModules drift: got=%v, want=%v\n"+
			"Update MANIFEST-SCHEMA.md §3.4 + this archtest in lockstep, then the slice.",
			allowed, expectedAllowedRawImports)
	}
	if !sliceEqualUnordered(core, expectedCoreHandleIDs) {
		t.Errorf("coreHandleIDs drift: got=%v, want=%v\n"+
			"The daemon's `alf:` namespace exposes EXACTLY these handle kinds. "+
			"Adding a kind requires a forge implementation alongside; removing "+
			"one breaks every existing manifest depending on it.",
			core, expectedCoreHandleIDs)
	}
}

// extractStringSlice scans the schema source for a `var <name> = []string{...}`
// declaration and returns the quoted strings in declaration order. A
// `var <name> = map[string]struct{}{...}` declaration is also supported
// (the keys are returned, in source order). Comments inside the literal
// are skipped.
//
// The regex-driven parse is deliberately simple — the schema source is
// stable, the literals never need to span dynamic boilerplate. If the
// source layout changes (e.g. multi-line literals with embedded calls),
// this helper rejects: the test fails loudly rather than silently
// missing a new entry.
//
// Note on `map[string]struct{}{...}`: Go syntax embeds a `struct{}`
// literal inside the type expression which contains a `{}` pair we
// must NOT count as the outer literal. We strip the type prefix
// `map[...]struct{}` (and similar inline-empty-struct shapes) before
// brace-counting so the depth tracker only sees the real literal.
func extractStringSlice(t *testing.T, src, varName string) []string {
	t.Helper()

	// Find the `<varName> = ...` line.
	startIdx := strings.Index(src, varName+" = ")
	if startIdx == -1 {
		t.Fatalf("variable %q not found in schema.go", varName)
	}
	// Slice from `=` to the end of file. The literal-stripping below
	// works on the post-= subsequence so `struct{}` in the type
	// expression doesn't trip the brace counter.
	eqIdx := startIdx + len(varName) + len(" = ")
	tail := src[eqIdx:]
	// Strip the embedded `struct{}` from `map[K]struct{}{...}` if
	// present so the brace counter sees only the outer literal. The
	// substitution is targeted: only the FIRST occurrence, and only
	// when followed by `{` (i.e. the start of a map-literal body).
	tail = strings.Replace(tail, "struct{}{", "ZSTRUCTBODY{", 1)
	openBrace := strings.Index(tail, "{")
	if openBrace == -1 {
		t.Fatalf("variable %q has no `{` in its literal", varName)
	}
	depth := 1
	closeIdx := -1
	for i := openBrace + 1; i < len(tail); i++ {
		switch tail[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				closeIdx = i
				break
			}
		}
		if closeIdx != -1 {
			break
		}
	}
	if closeIdx == -1 {
		t.Fatalf("variable %q has unbalanced braces", varName)
	}
	body := tail[openBrace+1 : closeIdx]

	// Match every `"..."` literal, in source order. Comments are
	// stripped first so a forbidden-string-in-a-comment can't sneak in.
	var out []string
	for _, line := range strings.Split(body, "\n") {
		// Strip line comment.
		if idx := strings.Index(line, "//"); idx != -1 {
			line = line[:idx]
		}
		// Find every quoted literal.
		matches := stringLitPattern.FindAllString(line, -1)
		out = append(out, matches...)
	}
	return out
}

var stringLitPattern = regexp.MustCompile(`"[^"]*"`)

func sliceEqualOrdered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sliceEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	want := make(map[string]int, len(b))
	for _, s := range b {
		want[s]++
	}
	for _, s := range a {
		want[s]--
		if want[s] < 0 {
			return false
		}
	}
	for _, count := range want {
		if count != 0 {
			return false
		}
	}
	return true
}
