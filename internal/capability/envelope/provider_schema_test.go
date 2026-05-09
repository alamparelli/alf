package envelope

import (
	"errors"
	"strings"
	"testing"
)

// validCapabilityProviderManifest returns a minimal manifest of kind
// "capability-provider" — used by provider-block tests to avoid
// repeating the kind switch in every TOML literal.
func validCapabilityProviderManifest() string {
	return `alf_envelope_version = 1
id      = "alf-bluetooth-provider"
kind    = "capability-provider"
version = "0.1.0"
name    = "Bluetooth Provider"
`
}

func TestValidate_ProviderExportsHappyPath(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"

[[provider.exports]]
id = "bluetooth.connect"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Kind != KindCapabilityProvider {
		t.Fatalf("Kind=%q, want capability-provider", m.Kind)
	}
	if len(m.Provider.Exports) != 2 {
		t.Fatalf("Provider.Exports len=%d, want 2", len(m.Provider.Exports))
	}
	if m.Provider.Exports[0].ID != "bluetooth.scan" || m.Provider.Exports[1].ID != "bluetooth.connect" {
		t.Errorf("Exports=%+v", m.Provider.Exports)
	}
}

// A [[provider.exports]] block on a non-provider kind is the load-bearing
// case the kind split exists to disambiguate. Without this rejection,
// any wasm-tool manifest could declare exports and the runtime would
// have no way to distinguish "this guest provides handle kinds" from
// "the author copy-pasted a [provider] block by mistake".
func TestValidate_ProviderBlockOnNonProviderKindRejected(t *testing.T) {
	otherKinds := []string{"wasm-tool", "wasm-app", "skill", "llm-provider", "marketplace-app"}
	for _, k := range otherKinds {
		input := `alf_envelope_version = 1
id      = "x"
kind    = "` + k + `"
version = "0.1.0"
name    = "X"

[[provider.exports]]
id = "x.kind"
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrProviderBlockNotAllowedHere) {
			t.Errorf("kind=%q: want ErrProviderBlockNotAllowedHere, got %v", k, err)
		}
	}
}

// Empty [provider] block (no exports) is allowed on any kind — only
// declared exports trigger the kind check. A provider manifest with no
// exports is a degenerate but legal case (placeholder during scaffold).
func TestValidate_EmptyProviderBlockOnAnyKindAccepted(t *testing.T) {
	for _, k := range []string{"wasm-tool", "capability-provider"} {
		input := `alf_envelope_version = 1
id      = "x"
kind    = "` + k + `"
version = "0.1.0"
name    = "X"

[provider]
`
		if _, err := Validate([]byte(input)); err != nil {
			t.Errorf("kind=%q: empty [provider] should be accepted, got %v", k, err)
		}
	}
}

func TestValidate_ProviderExportIDEmpty(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = ""
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrProviderExportIDEmpty) {
		t.Fatalf("want ErrProviderExportIDEmpty, got %v", err)
	}
}

func TestValidate_ProviderExportIDMalformed(t *testing.T) {
	bad := []string{
		"Bluetooth.Scan", // uppercase
		"-leading.dash",  // leading dash
		".leading.dot",   // leading dot
		"with_underscore",
		"with space",
	}
	for _, id := range bad {
		input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "` + id + `"
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrProviderExportIDMalformed) {
			t.Errorf("id=%q: want ErrProviderExportIDMalformed, got %v", id, err)
		}
	}
}

func TestValidate_ProviderExportIDDuplicate(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"

[[provider.exports]]
id = "bluetooth.scan"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrProviderExportDuplicate) {
		t.Fatalf("want ErrProviderExportDuplicate, got %v", err)
	}
}

// The legacy "provider" kind value (pre-#392 split) must NOT be
// silently accepted — old daemons would now interpret it ambiguously,
// new daemons must hard-fail so manifests authors are forced to pick
// llm-provider vs capability-provider explicitly.
func TestValidate_LegacyProviderKindRejected(t *testing.T) {
	input := strings.Replace(validManifest(), `kind    = "wasm-tool"`,
		`kind    = "provider"`, 1)
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrKindUnknown) {
		t.Fatalf("want ErrKindUnknown for legacy 'provider' kind, got %v", err)
	}
}

// Both new provider sub-kinds parse to their typed enum value.
func TestValidate_LLMProviderKindAccepted(t *testing.T) {
	input := strings.Replace(validManifest(), `kind    = "wasm-tool"`,
		`kind    = "llm-provider"`, 1)
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Kind != KindLLMProvider {
		t.Errorf("Kind=%q, want llm-provider", m.Kind)
	}
}

func TestValidate_CapabilityProviderKindAccepted(t *testing.T) {
	m, err := Validate([]byte(validCapabilityProviderManifest()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Kind != KindCapabilityProvider {
		t.Errorf("Kind=%q, want capability-provider", m.Kind)
	}
}

// #392 Stage 4 — scope_fields schema on provider exports.

func TestValidate_ProviderScopeFieldsHappyPath(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [
    { name = "device", type = "string", required = true },
    { name = "timeout_ms", type = "int", required = false },
    { name = "verbose", type = "bool", required = false },
    { name = "tags", type = "string-list", required = false },
    { name = "ports", type = "int-list", required = false },
]
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Provider.Exports) != 1 {
		t.Fatalf("Exports len=%d", len(m.Provider.Exports))
	}
	got := m.Provider.Exports[0].ScopeFields
	want := []ScopeField{
		{Name: "device", Type: ScopeFieldTypeString, Required: true},
		{Name: "timeout_ms", Type: ScopeFieldTypeInt, Required: false},
		{Name: "verbose", Type: ScopeFieldTypeBool, Required: false},
		{Name: "tags", Type: ScopeFieldTypeStringList, Required: false},
		{Name: "ports", Type: ScopeFieldTypeIntList, Required: false},
	}
	if len(got) != len(want) {
		t.Fatalf("ScopeFields len=%d, want %d", len(got), len(want))
	}
	for i, f := range want {
		if got[i] != f {
			t.Errorf("ScopeFields[%d]=%+v, want %+v", i, got[i], f)
		}
	}
}

func TestValidate_ProviderScopeFieldsAbsentYieldsNil(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Provider.Exports[0].ScopeFields != nil {
		t.Errorf("ScopeFields=%+v, want nil", m.Provider.Exports[0].ScopeFields)
	}
}

func TestValidate_ProviderScopeFieldNameEmpty(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [{ name = "", type = "string", required = false }]
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrScopeFieldNameEmpty) {
		t.Fatalf("want ErrScopeFieldNameEmpty, got %v", err)
	}
}

func TestValidate_ProviderScopeFieldNameMalformed(t *testing.T) {
	bad := []string{
		"With-Hyphen",
		"With.Dot",
		"with space",
		"UPPER",
		"1leading-digit",
		"-leading-dash",
		"_leading-underscore",
	}
	for _, n := range bad {
		input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [{ name = "` + n + `", type = "string", required = false }]
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrScopeFieldNameMalformed) {
			t.Errorf("name=%q: want ErrScopeFieldNameMalformed, got %v", n, err)
		}
	}
}

func TestValidate_ProviderScopeFieldTypeEmpty(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [{ name = "device", type = "", required = false }]
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrScopeFieldTypeEmpty) {
		t.Fatalf("want ErrScopeFieldTypeEmpty, got %v", err)
	}
}

func TestValidate_ProviderScopeFieldTypeUnknown(t *testing.T) {
	bad := []string{"float", "string-array", "object", "any", "i64", "STRING"}
	for _, ty := range bad {
		input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [{ name = "field", type = "` + ty + `", required = false }]
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrScopeFieldTypeUnknown) {
			t.Errorf("type=%q: want ErrScopeFieldTypeUnknown, got %v", ty, err)
		}
	}
}

func TestValidate_ProviderScopeFieldDuplicateName(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "bluetooth.scan"
scope_fields = [
    { name = "device", type = "string", required = true },
    { name = "device", type = "int", required = false },
]
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrScopeFieldDuplicate) {
		t.Fatalf("want ErrScopeFieldDuplicate, got %v", err)
	}
}

// scope_fields can differ between exports — duplicate-detection is
// per-export, not per-manifest.
func TestValidate_ProviderScopeFieldsPerExportIsolation(t *testing.T) {
	input := validCapabilityProviderManifest() + `
[[provider.exports]]
id = "scan"
scope_fields = [{ name = "device", type = "string", required = true }]

[[provider.exports]]
id = "connect"
scope_fields = [{ name = "device", type = "string", required = true }]
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v (different exports may share a field name)", err)
	}
	if len(m.Provider.Exports) != 2 {
		t.Fatalf("Exports len=%d", len(m.Provider.Exports))
	}
	if m.Provider.Exports[0].ScopeFields[0].Name != "device" ||
		m.Provider.Exports[1].ScopeFields[0].Name != "device" {
		t.Error("expected both exports to carry a `device` field")
	}
}
