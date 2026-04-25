package envelope

import (
	"errors"
	"strings"
	"testing"
)

func validManifest() string {
	return `alf_envelope_version = 1
id      = "hello-read"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Hello Read"

[[fs.reads]]
path = "data/"
`
}

func TestValidate_HappyPath(t *testing.T) {
	m, err := Validate([]byte(validManifest()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.ID != "hello-read" {
		t.Errorf("ID=%q, want hello-read", m.ID)
	}
	if m.Kind != KindWASMTool {
		t.Errorf("Kind=%q, want wasm-tool", m.Kind)
	}
	if m.EnvelopeVersion != EnvelopeVersion0_8_0 {
		t.Errorf("EnvelopeVersion=%d, want %d", m.EnvelopeVersion, EnvelopeVersion0_8_0)
	}
	if len(m.FS.Reads) != 1 || m.FS.Reads[0].Path != "data/" {
		t.Errorf("FS.Reads=%v, want one entry with path=data/", m.FS.Reads)
	}
}

func TestValidate_EnvelopeVersionMissing(t *testing.T) {
	input := `id = "x"
kind = "wasm-tool"
version = "0"
name = "X"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrEnvelopeVersionMissing) {
		t.Fatalf("want ErrEnvelopeVersionMissing, got %v", err)
	}
}

func TestValidate_EnvelopeVersionUnsupported(t *testing.T) {
	for _, v := range []int{0, 2, 99} {
		input := replaceLine(validManifest(), "alf_envelope_version = 1",
			"alf_envelope_version = "+itoa(v))
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrEnvelopeVersionUnsupported) {
			t.Errorf("version=%d: want ErrEnvelopeVersionUnsupported, got %v", v, err)
		}
	}
}

func TestValidate_IDMissing(t *testing.T) {
	input := `alf_envelope_version = 1
kind = "wasm-tool"
version = "0"
name = "X"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrIDMissing) {
		t.Fatalf("want ErrIDMissing, got %v", err)
	}
}

func TestValidate_IDMalformed(t *testing.T) {
	cases := []string{
		"Hello",         // uppercase
		"hello_world",   // underscore
		"-leading-dash", // starts with dash
		"a b c",         // spaces
		"",              // empty handled by IDMissing — skip here
	}
	for _, badID := range cases {
		if badID == "" {
			continue
		}
		input := strings.Replace(validManifest(), `id      = "hello-read"`,
			`id      = "`+badID+`"`, 1)
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrIDMalformed) {
			t.Errorf("id=%q: want ErrIDMalformed, got %v", badID, err)
		}
	}
}

func TestValidate_KindUnknown(t *testing.T) {
	input := strings.Replace(validManifest(), `kind    = "wasm-tool"`,
		`kind    = "mystery-kind"`, 1)
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrKindUnknown) {
		t.Fatalf("want ErrKindUnknown, got %v", err)
	}
}

func TestValidate_VersionMissing(t *testing.T) {
	input := strings.Replace(validManifest(), `version = "0.1.0"`, "", 1)
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrVersionMissing) {
		t.Fatalf("want ErrVersionMissing, got %v", err)
	}
}

func TestValidate_NameMissing(t *testing.T) {
	input := strings.Replace(validManifest(), `name    = "Hello Read"`, "", 1)
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrNameMissing) {
		t.Fatalf("want ErrNameMissing, got %v", err)
	}
}

func TestValidate_UnknownFieldRejected(t *testing.T) {
	// Append as a top-level field BEFORE any [[table]] section so the
	// TOML parser places it at the top level, not inside fs.reads.
	input := `alf_envelope_version = 1
id      = "hello-read"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Hello Read"
author  = "someone"

[[fs.reads]]
path = "data/"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("want ErrUnknownField, got %v", err)
	}
	if !strings.Contains(err.Error(), `"author"`) {
		t.Errorf("error should name the offending field, got %v", err)
	}
}

func TestValidate_DeferredBlocksRejected(t *testing.T) {
	// `events` no longer deferred — landed under #399. See
	// TestValidate_EventsBlock_* for the events-block coverage.
	// `tools` no longer deferred — landed under #389. See
	// TestValidate_ToolsBlock_* for the tools-block coverage.
	cases := map[string]string{
		"http":    "[[http.scopes]]\nhost = \"x.com\"\n",
		"exec":    "[[exec.commands]]\npath = \"/bin/x\"\n",
		"secrets": "[[secrets.scopes]]\nname = \"x\"\n",
		"memory":  "[memory]\nscope = \"x\"\n",
	}
	for blk, snippet := range cases {
		input := validManifest() + "\n" + snippet
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrBlockDeferred) {
			t.Errorf("block=%s: want ErrBlockDeferred, got %v", blk, err)
		}
	}
}

func TestValidate_FSPathAbsoluteRejected(t *testing.T) {
	input := strings.Replace(validManifest(), `path = "data/"`,
		`path = "/etc/passwd"`, 1)
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrFSPathAbsolute) {
		t.Fatalf("want ErrFSPathAbsolute, got %v", err)
	}
}

func TestValidate_FSPathTraversalRejected(t *testing.T) {
	for _, bad := range []string{"../secret", "a/../b", "a/b/.."} {
		input := strings.Replace(validManifest(), `path = "data/"`,
			`path = "`+bad+`"`, 1)
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrFSPathTraversal) {
			t.Errorf("path=%q: want ErrFSPathTraversal, got %v", bad, err)
		}
	}
}

func TestValidate_FSPathEmptyRejected(t *testing.T) {
	input := strings.Replace(validManifest(), `path = "data/"`, `path = ""`, 1)
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrFSPathEmpty) {
		t.Fatalf("want ErrFSPathEmpty, got %v", err)
	}
}

func TestValidate_NoFSBlockAllowed(t *testing.T) {
	// A capability that declares no filesystem access is valid — the
	// forge produces a nil FS handle, the cap has no fs authority.
	input := `alf_envelope_version = 1
id = "no-fs"
kind = "wasm-tool"
version = "0.1.0"
name = "No FS"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.FS.Reads) != 0 || len(m.FS.Writes) != 0 {
		t.Errorf("FS block should be empty, got %+v", m.FS)
	}
}

func TestValidate_AllKinds(t *testing.T) {
	for kindStr, kindConst := range knownKinds {
		input := strings.Replace(validManifest(), `kind    = "wasm-tool"`,
			`kind    = "`+kindStr+`"`, 1)
		m, err := Validate([]byte(input))
		if err != nil {
			t.Errorf("kind=%s: Validate failed: %v", kindStr, err)
			continue
		}
		if m.Kind != kindConst {
			t.Errorf("kind=%s: Manifest.Kind=%q", kindStr, m.Kind)
		}
	}
}

func TestValidate_ToolsBlock_HappyPath(t *testing.T) {
	input := validManifest() + `
[[tools.declares]]
id = "read-file"

[[tools.declares]]
id = "bash"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Tools.Declares) != 2 {
		t.Fatalf("Tools.Declares=%d, want 2", len(m.Tools.Declares))
	}
	if m.Tools.Declares[0].ID != "read-file" || m.Tools.Declares[1].ID != "bash" {
		t.Errorf("declares=%v, want [read-file, bash]", m.Tools.Declares)
	}
}

func TestValidate_ToolsBlock_NoBlockAllowed(t *testing.T) {
	// Skill / tool with no [[tools.declares]] is valid — the forge
	// produces a nil ToolHandle, the cap cannot invoke any other.
	m, err := Validate([]byte(validManifest()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Tools.Declares) != 0 {
		t.Errorf("Tools.Declares should be empty, got %v", m.Tools.Declares)
	}
}

func TestValidate_ToolsBlock_EmptyIDRejected(t *testing.T) {
	input := validManifest() + `
[[tools.declares]]
id = ""
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolDeclareIDEmpty) {
		t.Fatalf("want ErrToolDeclareIDEmpty, got %v", err)
	}
}

func TestValidate_ToolsBlock_MalformedIDRejected(t *testing.T) {
	for _, bad := range []string{"UPPER", "with_underscore", "-leading-dash", "with.dot"} {
		input := validManifest() + `
[[tools.declares]]
id = "` + bad + `"
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrToolDeclareIDMalformed) {
			t.Errorf("id=%q: want ErrToolDeclareIDMalformed, got %v", bad, err)
		}
	}
}

func TestValidate_ToolsBlock_DuplicateIDRejected(t *testing.T) {
	input := validManifest() + `
[[tools.declares]]
id = "bash"

[[tools.declares]]
id = "bash"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolDeclareDuplicate) {
		t.Fatalf("want ErrToolDeclareDuplicate, got %v", err)
	}
}

// helpers

func replaceLine(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 4)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
