package envelope

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// sampleManifest is the minimal-valid manifest shape from MANIFEST-SCHEMA
// §9. Canonicalization is agnostic to semantics — that's schema.go's job —
// so the tests here exercise the byte-level contract: idempotency,
// format-insensitivity, key ordering, NFC normalisation.
const sampleManifest = `alf_envelope_version = 1
id          = "hello-read"
kind        = "wasm-tool"
version     = "0.1.0"
name        = "Hello Read"
description = "Reads a file from the capability's scoped data dir."

[[fs.reads]]
path = "data/"
`

func TestCanonicalize_Basic(t *testing.T) {
	out, err := Canonicalize([]byte(sampleManifest))
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	// Quick structural sanity — it must parse back as JSON.
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("canonical output is not valid JSON: %v\noutput: %s", err, out)
	}
	if back["id"] != "hello-read" {
		t.Errorf("round-trip id=%v, want hello-read", back["id"])
	}
}

func TestCanonicalize_Idempotent(t *testing.T) {
	// Calling Canonicalize on its own output (wrapped as a TOML literal
	// of the same logical content) must be a fixed point when the logical
	// content is equivalent. We approximate this property by calling
	// Canonicalize twice on the SAME input — outputs must be identical.
	// Full idempotency (canonicalize of canonical form re-parsed) is a
	// cross-format contract better exercised by the format-insensitivity
	// test below.
	first, err := Canonicalize([]byte(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Canonicalize([]byte(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("Canonicalize is not deterministic:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestCanonicalize_FormatInsensitive(t *testing.T) {
	// Same logical manifest, different surface form — extra whitespace,
	// comments, reshuffled top-level keys. Must produce identical bytes.
	reshuffled := `# this is a comment about the manifest
kind = "wasm-tool"

# version lives here, but who cares
version = "0.1.0"

alf_envelope_version = 1
description = "Reads a file from the capability's scoped data dir."
name        = "Hello Read"
id          = "hello-read"

[[fs.reads]]
    path = "data/"
`

	a, err := Canonicalize([]byte(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonicalize([]byte(reshuffled))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("format-insensitive expectation violated:\nA: %s\nB: %s", a, b)
	}
}

func TestCanonicalize_KeysSortedLexicographically(t *testing.T) {
	input := `zeta = "z"
alpha = "a"
mu = "m"
`
	out, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// We expect {"alpha":"a","mu":"m","zeta":"z"} — check substring order.
	posA := strings.Index(s, `"alpha"`)
	posM := strings.Index(s, `"mu"`)
	posZ := strings.Index(s, `"zeta"`)
	if !(posA < posM && posM < posZ) {
		t.Errorf("keys not lexicographically sorted, got: %s", s)
	}
}

func TestCanonicalize_NestedKeysSorted(t *testing.T) {
	input := `[outer]
zeta = "z"
alpha = "a"
`
	out, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Inside the outer object, alpha must appear before zeta.
	posA := strings.Index(s, `"alpha"`)
	posZ := strings.Index(s, `"zeta"`)
	if !(posA < posZ) {
		t.Errorf("nested keys not sorted: %s", s)
	}
}

func TestCanonicalize_NFCNormalisation(t *testing.T) {
	// U+00E9 (é as one codepoint, NFC) vs U+0065 U+0301 (e + combining
	// acute, NFD). Both represent the same grapheme; after NFC they
	// produce identical bytes.
	nfc := "café" // one-codepoint é
	nfd := "café"
	if nfc == nfd {
		t.Fatal("test setup: NFC/NFD forms are byte-equal, cannot exercise normalisation")
	}

	manifestNFC := `id = "` + nfc + `"
alf_envelope_version = 1
`
	manifestNFD := `id = "` + nfd + `"
alf_envelope_version = 1
`
	a, err := Canonicalize([]byte(manifestNFC))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonicalize([]byte(manifestNFD))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("NFC normalisation failed:\nNFC: %s\nNFD: %s", a, b)
	}
}

func TestCanonicalize_NoTrailingNewline(t *testing.T) {
	out, err := Canonicalize([]byte(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || out[len(out)-1] == '\n' {
		t.Errorf("canonical output has trailing newline (JCS forbids)")
	}
}

func TestCanonicalize_ArraysOfTables(t *testing.T) {
	input := `[[fs.reads]]
path = "data/"

[[fs.reads]]
path = "config.json"
`
	out, err := Canonicalize([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	// Expect shape: {"fs":{"reads":[{"path":"data/"},{"path":"config.json"}]}}
	// Order of array elements must be preserved (TOML spec: arrays keep order).
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("output not valid JSON: %v — %s", err, out)
	}
	fs, ok := back["fs"].(map[string]any)
	if !ok {
		t.Fatalf("expected fs object, got %T", back["fs"])
	}
	reads, ok := fs["reads"].([]any)
	if !ok || len(reads) != 2 {
		t.Fatalf("expected 2 reads, got %v", fs["reads"])
	}
	first, _ := reads[0].(map[string]any)
	second, _ := reads[1].(map[string]any)
	if first["path"] != "data/" || second["path"] != "config.json" {
		t.Errorf("array order not preserved: %v", reads)
	}
}

func TestCanonicalize_InvalidTOMLRejected(t *testing.T) {
	input := `this is not valid TOML = = =`
	_, err := Canonicalize([]byte(input))
	if err == nil {
		t.Fatal("expected error on invalid TOML, got nil")
	}
}

func TestCanonicalize_EmptyInput(t *testing.T) {
	out, err := Canonicalize([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	// Empty TOML → empty object.
	if string(out) != "{}" {
		t.Errorf("empty input canonical=%q, want {}", out)
	}
}
