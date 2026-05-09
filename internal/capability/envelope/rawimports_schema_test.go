package envelope

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate_RawImportsHappyPath(t *testing.T) {
	input := validManifest() + `
[[raw_imports]]
module        = "wasi:clocks/monotonic-clock"
function      = "now"
justification = "high-frequency animation timing"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.RawImports) != 1 {
		t.Fatalf("RawImports len=%d, want 1", len(m.RawImports))
	}
	got := m.RawImports[0]
	if got.Module != "wasi:clocks/monotonic-clock" || got.Function != "now" {
		t.Errorf("RawImports[0]=%+v", got)
	}
}

// Acceptance criterion #4 of #392: Capability with raw import
// `wasi:filesystem/types/descriptor/read` → rejected at verify time.
// This is the load-bearing forbidden-list pin.
func TestValidate_RawImportsFilesystemForbidden(t *testing.T) {
	input := validManifest() + `
[[raw_imports]]
module        = "wasi:filesystem/types/descriptor"
function      = "read"
justification = "naive file reading"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrRawImportForbidden) {
		t.Fatalf("want ErrRawImportForbidden, got %v", err)
	}
}

// Defence in depth: every prefix in forbiddenRawImportModules rejects.
func TestValidate_RawImportsForbiddenList(t *testing.T) {
	cases := []struct {
		module string
		fn     string
	}{
		{"wasi:filesystem/types/descriptor", "read"},
		{"wasi:filesystem/preopens", "get-directories"},
		{"wasi:sockets/tcp", "bind"},
		{"wasi:sockets/tcp", "connect"},
		{"wasi:sockets/udp", "bind"},
		{"wasi:random/random", "get-random-bytes"},
		{"wasi:io/streams", "read"},
	}
	for _, c := range cases {
		input := validManifest() + `
[[raw_imports]]
module        = "` + c.module + `"
function      = "` + c.fn + `"
justification = "test"
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrRawImportForbidden) {
			t.Errorf("module=%q: want ErrRawImportForbidden, got %v", c.module, err)
		}
	}
}

// The default-deny rule: a syntactically valid module not in either
// allowlist nor forbidden list fails with NotInAllowlist. This blocks
// "I'll just declare the module name and hope the daemon recognises it"
// authoring patterns.
func TestValidate_RawImportsUnknownModuleRejected(t *testing.T) {
	input := validManifest() + `
[[raw_imports]]
module        = "wasi:custom/whatever"
function      = "x"
justification = "test"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrRawImportNotInAllowlist) {
		t.Fatalf("want ErrRawImportNotInAllowlist, got %v", err)
	}
}

func TestValidate_RawImportsModuleEmpty(t *testing.T) {
	input := validManifest() + `
[[raw_imports]]
module        = ""
function      = "now"
justification = "test"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrRawImportModuleEmpty) {
		t.Fatalf("want ErrRawImportModuleEmpty, got %v", err)
	}
}

func TestValidate_RawImportsModuleMalformed(t *testing.T) {
	bad := []string{
		"no-scheme",        // missing wasi: prefix
		"wasi:UPPER/case",  // uppercase
		"wasi:",            // empty package
		"wasi:-leading",    // leading dash after wasi:
		"http:something",   // non-wasi scheme
	}
	for _, mod := range bad {
		input := validManifest() + `
[[raw_imports]]
module        = "` + mod + `"
function      = "x"
justification = "test"
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrRawImportModuleMalformed) {
			t.Errorf("module=%q: want ErrRawImportModuleMalformed, got %v", mod, err)
		}
	}
}

func TestValidate_RawImportsFunctionEmpty(t *testing.T) {
	input := validManifest() + `
[[raw_imports]]
module        = "wasi:clocks/monotonic-clock"
function      = ""
justification = "test"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrRawImportFunctionEmpty) {
		t.Fatalf("want ErrRawImportFunctionEmpty, got %v", err)
	}
}

func TestValidate_RawImportsFunctionMalformed(t *testing.T) {
	input := validManifest() + `
[[raw_imports]]
module        = "wasi:clocks/monotonic-clock"
function      = "with.dot"
justification = "test"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrRawImportFunctionMalformed) {
		t.Fatalf("want ErrRawImportFunctionMalformed, got %v", err)
	}
}

// Justification is operator-facing. Empty (or whitespace-only) means
// the bundle author skipped the explanation — refuse the manifest so
// they can't slip raw access past the install prompt.
func TestValidate_RawImportsJustificationEmpty(t *testing.T) {
	for _, j := range []string{"", "   ", "\t\n  "} {
		input := validManifest() + `
[[raw_imports]]
module        = "wasi:clocks/monotonic-clock"
function      = "now"
justification = ` + tomlString(j) + `
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrRawImportJustificationEmpty) {
			t.Errorf("justification=%q: want ErrRawImportJustificationEmpty, got %v", j, err)
		}
	}
}

func TestValidate_RawImportsAbsentYieldsNil(t *testing.T) {
	m, err := Validate([]byte(validManifest()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.RawImports != nil {
		t.Errorf("RawImports=%+v, want nil", m.RawImports)
	}
}

// classifyRawImport is the unexported classifier — pin its behaviour
// directly so the archtest in internal/archtest/ has a known-good
// reference to verify against. This is the second half of the
// "no-bypass-of-allowlist" pin: external callers can't construct their
// own classifier; this test ensures the one classifier in the package
// stays consistent with the documented sets.
func TestClassifyRawImport_ForbiddenWins(t *testing.T) {
	// Prefix that matches BOTH a forbidden and an allowed entry would
	// be rejected; if a future allowlist entry incidentally subsumes a
	// forbidden one, the classifier returns forbidden first.
	cases := []struct {
		mod      string
		want     rawImportClass
	}{
		{"wasi:clocks/monotonic-clock", rawImportAllowed},
		{"wasi:filesystem/types/descriptor", rawImportForbidden},
		{"wasi:sockets/tcp", rawImportForbidden},
		{"wasi:custom/whatever", rawImportUnknown},
		{"", rawImportUnknown},
	}
	for _, c := range cases {
		if got := classifyRawImport(c.mod); got != c.want {
			t.Errorf("classifyRawImport(%q)=%v, want %v", c.mod, got, c.want)
		}
	}
}

// tomlString returns a TOML-quoted string literal. Used by tests that
// need to embed whitespace-only or special-char strings directly into
// the manifest TOML without TOML parsing them as bare strings.
func tomlString(s string) string {
	// Use TOML's basic-string form. None of the test strings contain
	// embedded quotes / backslashes, so direct quoting suffices.
	return `"` + strings.ReplaceAll(s, "\n", `\n`) + `"`
}
