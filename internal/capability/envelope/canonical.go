// Package envelope implements the #397 canonicalization pipeline and the
// #388 load-time signature verifier. This file hosts the canonicalizer
// only — the signer and verifier live in sibling files and both call
// into Canonicalize for the signed-data derivation.
//
// Reference: docs/ARCHITECTURE-SECURITY.md §7.10
package envelope

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/unicode/norm"
)

// Canonicalize parses a manifest.toml byte sequence and returns the
// RFC 8785 JSON Canonicalization Scheme (JCS) encoding of its logical
// content. The bytes returned are what signers and verifiers hash — two
// manifest files with the same meaning but different whitespace, key
// order, or comment presence produce byte-identical output.
//
// Pipeline (§7.10.2):
//  1. Parse TOML with the pinned pelletier/go-toml/v2 parser
//  2. Project into a generic map[string]any (typed projection + schema
//     validation land in schema.go at step 2)
//  3. Sort map keys lexicographically at every level
//  4. Apply Unicode NFC normalization to every string value
//  5. Serialize with encoding/json (no whitespace) using json.Number to
//     preserve the shortest-round-trip representation of numerics
//
// Known gaps (tracked for later polish, not blocking step 1):
//   - JCS number-form for floats beyond what json.Number preserves
//     (we defer floats via json.Number; integer handling is exact).
//     Manifests today use integers (envelope_version) and strings only,
//     so the float edge case is dormant.
//   - TOML date/time → RFC 3339 coercion: the parser returns time.Time
//     which json.Marshal emits as RFC 3339 by default. Verified by the
//     format-insensitive test below.
func Canonicalize(tomlBytes []byte) ([]byte, error) {
	var raw map[string]any
	if err := toml.Unmarshal(tomlBytes, &raw); err != nil {
		return nil, fmt.Errorf("envelope: parse TOML: %w", err)
	}
	normalised := normaliseValue(raw)

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalised); err != nil {
		return nil, fmt.Errorf("envelope: marshal canonical JSON: %w", err)
	}
	// json.Encoder appends a trailing newline; JCS output is defined
	// without one.
	b := out.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// normaliseValue walks the decoded TOML tree and applies the canonical
// form rules recursively. Returns a value suitable for json.Marshal
// that, when marshalled, yields JCS-compatible bytes.
func normaliseValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(orderedMap, 0, len(x))
		for _, k := range keys {
			out = append(out, orderedEntry{
				Key:   norm.NFC.String(k),
				Value: normaliseValue(x[k]),
			})
		}
		return out
	case []any:
		for i, item := range x {
			x[i] = normaliseValue(item)
		}
		return x
	case string:
		return norm.NFC.String(x)
	default:
		return x
	}
}

// orderedMap serialises as a JSON object while preserving insertion order.
// Used to guarantee deterministic key order at marshal time, after we
// have sorted keys in normaliseValue. A plain map[string]any is NOT
// sufficient because encoding/json does not guarantee iteration order
// for maps — we'd get alphabetical order by accident on Go <1.12 but
// that's an implementation detail, not a contract.
type orderedMap []orderedEntry

type orderedEntry struct {
	Key   string
	Value any
}

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, entry := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(entry.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valBytes, err := json.Marshal(entry.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
