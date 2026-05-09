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
