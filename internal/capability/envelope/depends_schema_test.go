package envelope

import (
	"errors"
	"testing"
)

func TestValidate_DependsHappyPath_CoreNamespace(t *testing.T) {
	input := validManifest() + `
[[depends]]
handle = "alf:fs"

[[depends]]
handle = "alf:tool"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Depends) != 2 {
		t.Fatalf("Depends len=%d, want 2", len(m.Depends))
	}
	if m.Depends[0].Handle != "alf:fs" || m.Depends[1].Handle != "alf:tool" {
		t.Errorf("Depends=%+v", m.Depends)
	}
}

// Provider-fingerprint namespace passes format validation in Stage 1;
// Stage 3 will look up the concrete provider in the registry.
func TestValidate_DependsHappyPath_ProviderNamespace(t *testing.T) {
	input := validManifest() + `
[[depends]]
handle = "abc123:bluetooth.scan"
scope  = { devices = ["my-thermostat"] }
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Depends) != 1 {
		t.Fatalf("Depends len=%d, want 1", len(m.Depends))
	}
	if m.Depends[0].Handle != "abc123:bluetooth.scan" {
		t.Errorf("Depends[0].Handle=%q", m.Depends[0].Handle)
	}
	devs, ok := m.Depends[0].Scope["devices"].([]any)
	if !ok || len(devs) != 1 {
		t.Errorf("Scope=%+v, want devices array", m.Depends[0].Scope)
	}
}

func TestValidate_DependsHandleEmpty(t *testing.T) {
	input := validManifest() + `
[[depends]]
handle = ""
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrDependsHandleEmpty) {
		t.Fatalf("want ErrDependsHandleEmpty, got %v", err)
	}
}

func TestValidate_DependsHandleMalformed(t *testing.T) {
	bad := []string{
		"no-colon",                    // missing namespace separator
		":bare-id",                    // empty namespace
		"namespace:",                  // empty id
		"WITH:CAPS",                   // uppercase
		"with.dots:in-ns",             // dots not allowed in namespace
		"a b:c",                       // space in namespace
		"ns:id:extra",                 // extra colons
	}
	for _, h := range bad {
		input := validManifest() + `
[[depends]]
handle = "` + h + `"
`
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrDependsHandleMalformed) {
			t.Errorf("handle=%q: want ErrDependsHandleMalformed, got %v", h, err)
		}
	}
}

// alf: namespace is reserved — only known core handle ids may appear
// after the prefix. This pins one of the load-bearing #392 invariants:
// no provider can claim a core kind via collision (the only path to
// alf:fs is via the daemon's bundled forge code, not a manifest).
func TestValidate_DependsAlfNamespaceReservedToCoreKinds(t *testing.T) {
	input := validManifest() + `
[[depends]]
handle = "alf:bluetooth.scan"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrDependsHandleNamespaceReserved) {
		t.Fatalf("want ErrDependsHandleNamespaceReserved, got %v", err)
	}
}

// Every documented core handle id must be accepted under alf:.
func TestValidate_DependsAlfNamespaceAcceptsAllCoreKinds(t *testing.T) {
	for _, id := range []string{"fs", "http", "exec", "secrets", "events.pub", "events.sub", "tool"} {
		input := validManifest() + `
[[depends]]
handle = "alf:` + id + `"
`
		if _, err := Validate([]byte(input)); err != nil {
			t.Errorf("alf:%s: want accepted, got %v", id, err)
		}
	}
}

func TestValidate_DependsDuplicate(t *testing.T) {
	input := validManifest() + `
[[depends]]
handle = "alf:fs"

[[depends]]
handle = "alf:fs"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrDependsDuplicate) {
		t.Fatalf("want ErrDependsDuplicate, got %v", err)
	}
}

// The Scope field is opaque at Stage 1 — any TOML table is accepted
// and copied through. Stage 4 of #392 will run schema validation
// against the provider's exported scope schema.
func TestValidate_DependsScopeIsOpaque(t *testing.T) {
	input := validManifest() + `
[[depends]]
handle = "abc123:custom.kind"

[depends.scope]
arbitrary = "string"
nested    = { count = 3, names = ["a", "b"] }
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Depends) != 1 {
		t.Fatalf("Depends len=%d", len(m.Depends))
	}
	scope := m.Depends[0].Scope
	if scope["arbitrary"] != "string" {
		t.Errorf("Scope[arbitrary]=%v", scope["arbitrary"])
	}
}

// Empty [[depends]] absent from the manifest yields a nil slice, not
// an empty one. This matches the "absent vs present-but-zero"
// canonicalization rule §7.10 imposes.
func TestValidate_DependsAbsentYieldsNil(t *testing.T) {
	m, err := Validate([]byte(validManifest()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Depends != nil {
		t.Errorf("Depends=%+v, want nil", m.Depends)
	}
}
