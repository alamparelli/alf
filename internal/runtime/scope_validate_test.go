package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/capability/handle"
)

// validateScopeAgainstSchema is the runtime hook for the M8 audit
// finding. These tests pin its behaviour directly; the
// InstantiateVerified-level integration tests below exercise the same
// code path through the verify→forge pipeline.

func TestValidateScopeAgainstSchema_BothEmpty(t *testing.T) {
	if err := validateScopeAgainstSchema(nil, nil); err != nil {
		t.Errorf("nil/nil: want pass, got %v", err)
	}
	if err := validateScopeAgainstSchema(map[string]any{}, nil); err != nil {
		t.Errorf("empty/nil: want pass, got %v", err)
	}
	if err := validateScopeAgainstSchema(nil, []handle.ScopeField{}); err != nil {
		t.Errorf("nil/empty: want pass, got %v", err)
	}
}

// Scope present but no schema declared → fail. The provider's
// interface accepts no parameters; consumer is passing data anyway.
func TestValidateScopeAgainstSchema_NonEmptyScopeNoSchema(t *testing.T) {
	err := validateScopeAgainstSchema(
		map[string]any{"any": "value"},
		nil,
	)
	if !errors.Is(err, ErrDependsScopeNonEmptyButNoSchema) {
		t.Fatalf("want ErrDependsScopeNonEmptyButNoSchema, got %v", err)
	}
}

func TestValidateScopeAgainstSchema_RequiredFieldMissing(t *testing.T) {
	schema := []handle.ScopeField{
		{Name: "device", Type: handle.ScopeFieldTypeString, Required: true},
	}
	err := validateScopeAgainstSchema(map[string]any{}, schema)
	if !errors.Is(err, ErrDependsScopeRequiredFieldMissing) {
		t.Fatalf("want ErrDependsScopeRequiredFieldMissing, got %v", err)
	}
	if !strings.Contains(err.Error(), `field="device"`) {
		t.Errorf("error message should reference the missing field: %v", err)
	}
}

func TestValidateScopeAgainstSchema_OptionalFieldAbsentOK(t *testing.T) {
	schema := []handle.ScopeField{
		{Name: "device", Type: handle.ScopeFieldTypeString, Required: true},
		{Name: "verbose", Type: handle.ScopeFieldTypeBool, Required: false},
	}
	scope := map[string]any{"device": "thermostat"}
	if err := validateScopeAgainstSchema(scope, schema); err != nil {
		t.Errorf("optional field absence: want pass, got %v", err)
	}
}

func TestValidateScopeAgainstSchema_UnknownField(t *testing.T) {
	schema := []handle.ScopeField{
		{Name: "device", Type: handle.ScopeFieldTypeString, Required: true},
	}
	scope := map[string]any{"device": "x", "rogue": "value"}
	err := validateScopeAgainstSchema(scope, schema)
	if !errors.Is(err, ErrDependsScopeUnknownField) {
		t.Fatalf("want ErrDependsScopeUnknownField, got %v", err)
	}
	if !strings.Contains(err.Error(), `field="rogue"`) {
		t.Errorf("error message should reference the unknown field: %v", err)
	}
}

func TestValidateScopeAgainstSchema_TypeChecks(t *testing.T) {
	cases := []struct {
		name    string
		fieldT  handle.ScopeFieldType
		good    any
		bad     any
	}{
		{"string", handle.ScopeFieldTypeString, "hello", int64(42)},
		{"int", handle.ScopeFieldTypeInt, int64(42), "not-int"},
		{"bool", handle.ScopeFieldTypeBool, true, "true-string"},
		{"string-list", handle.ScopeFieldTypeStringList, []any{"a", "b"}, []any{"a", int64(1)}},
		{"int-list", handle.ScopeFieldTypeIntList, []any{int64(1), int64(2)}, []any{int64(1), "two"}},
		{"int-list-not-list", handle.ScopeFieldTypeIntList, []any{int64(1)}, "not-a-list"},
	}
	for _, c := range cases {
		t.Run(c.name+"-good", func(t *testing.T) {
			schema := []handle.ScopeField{{Name: "f", Type: c.fieldT, Required: true}}
			err := validateScopeAgainstSchema(map[string]any{"f": c.good}, schema)
			if err != nil {
				t.Errorf("want pass for good %s, got %v", c.name, err)
			}
		})
		t.Run(c.name+"-bad", func(t *testing.T) {
			schema := []handle.ScopeField{{Name: "f", Type: c.fieldT, Required: true}}
			err := validateScopeAgainstSchema(map[string]any{"f": c.bad}, schema)
			if !errors.Is(err, ErrDependsScopeFieldTypeMismatch) {
				t.Errorf("%s with bad value: want ErrDependsScopeFieldTypeMismatch, got %v", c.name, err)
			}
		})
	}
}

// Multiple required fields — all missing reports the first one.
// Order is schema-declaration order so callers can fix sequentially.
func TestValidateScopeAgainstSchema_MultipleRequiredReportsFirst(t *testing.T) {
	schema := []handle.ScopeField{
		{Name: "alpha", Type: handle.ScopeFieldTypeString, Required: true},
		{Name: "beta", Type: handle.ScopeFieldTypeString, Required: true},
	}
	err := validateScopeAgainstSchema(map[string]any{}, schema)
	if !errors.Is(err, ErrDependsScopeRequiredFieldMissing) {
		t.Fatalf("want missing-required, got %v", err)
	}
	if !strings.Contains(err.Error(), `field="alpha"`) {
		t.Errorf("expected first missing field 'alpha' in error, got %v", err)
	}
}

// #392 Stage 4 acceptance criterion: scope validation runs Runtime-
// side, not in provider code. The InstantiateVerified flow drives
// validateScopeAgainstSchema as part of resolveDepends — pin both
// happy and failure paths through the full pipeline.

const dependsValidScopeManifest = `alf_envelope_version = 1
id      = "scope-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Scope Consumer"
`

const providerWithScopeManifest = `alf_envelope_version = 1
id      = "scope-prov"
kind    = "capability-provider"
version = "0.1.0"
name    = "Scope Provider"

[[provider.exports]]
id = "thing"
scope_fields = [
    { name = "device", type = "string", required = true },
    { name = "timeout_ms", type = "int", required = false },
]
`

func TestInstantiateVerified_ScopeValidates_HappyPath(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	provIn, store := signBundle(t, providerWithScopeManifest, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer provVI.Instance.Close()

	ns := provVI.SignerID.HexLower()
	consumer := dependsValidScopeManifest + `
[[depends]]
handle = "` + ns + `:thing"

[depends.scope]
device = "thermostat-A"
timeout_ms = 5000
`
	in := signBundleWithStore(t, consumer, nil, store)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer vi.Instance.Close()
}

func TestInstantiateVerified_ScopeValidates_RequiredMissing(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	provIn, store := signBundle(t, providerWithScopeManifest, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer provVI.Instance.Close()

	ns := provVI.SignerID.HexLower()
	// Missing the required `device` field.
	consumer := dependsValidScopeManifest + `
[[depends]]
handle = "` + ns + `:thing"

[depends.scope]
timeout_ms = 5000
`
	in := signBundleWithStore(t, consumer, nil, store)
	_, err = inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, ErrDependsScopeRequiredFieldMissing) {
		t.Fatalf("want ErrDependsScopeRequiredFieldMissing, got %v", err)
	}
}

func TestInstantiateVerified_ScopeValidates_TypeMismatch(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	provIn, store := signBundle(t, providerWithScopeManifest, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer provVI.Instance.Close()

	ns := provVI.SignerID.HexLower()
	// timeout_ms declared int, consumer passes string.
	consumer := dependsValidScopeManifest + `
[[depends]]
handle = "` + ns + `:thing"

[depends.scope]
device = "thermostat"
timeout_ms = "5000ms"
`
	in := signBundleWithStore(t, consumer, nil, store)
	_, err = inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, ErrDependsScopeFieldTypeMismatch) {
		t.Fatalf("want ErrDependsScopeFieldTypeMismatch, got %v", err)
	}
}

func TestInstantiateVerified_ScopeValidates_UnknownField(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	provIn, store := signBundle(t, providerWithScopeManifest, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer provVI.Instance.Close()

	ns := provVI.SignerID.HexLower()
	consumer := dependsValidScopeManifest + `
[[depends]]
handle = "` + ns + `:thing"

[depends.scope]
device = "thermostat"
rogue_param = "value"
`
	in := signBundleWithStore(t, consumer, nil, store)
	_, err = inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, ErrDependsScopeUnknownField) {
		t.Fatalf("want ErrDependsScopeUnknownField, got %v", err)
	}
}

// A capability-provider with no scope_fields, consumer passing scope.
// Should fail — ErrDependsScopeNonEmptyButNoSchema.
func TestInstantiateVerified_ScopeValidates_ScopeForFieldlessProvider(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	prov := `alf_envelope_version = 1
id      = "no-scope-prov"
kind    = "capability-provider"
version = "0.1.0"
name    = "No Scope"

[[provider.exports]]
id = "thing"
`
	provIn, store := signBundle(t, prov, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer provVI.Instance.Close()

	ns := provVI.SignerID.HexLower()
	consumer := dependsValidScopeManifest + `
[[depends]]
handle = "` + ns + `:thing"

[depends.scope]
device = "anything"
`
	in := signBundleWithStore(t, consumer, nil, store)
	_, err = inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, ErrDependsScopeNonEmptyButNoSchema) {
		t.Fatalf("want ErrDependsScopeNonEmptyButNoSchema, got %v", err)
	}
}

// alf:* core kinds carry no scope_fields (nil registered). A consumer
// that depends on alf:fs with NO scope passes; with non-empty scope
// fails as ScopeNonEmptyButNoSchema.
func TestInstantiateVerified_ScopeValidates_AlfCoreNoScopeAccepted(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	manifest := `alf_envelope_version = 1
id      = "alf-fs-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "C"

[[depends]]
handle = "alf:fs"
`
	in, _ := signBundle(t, manifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("alf:fs without scope: %v", err)
	}
	defer vi.Instance.Close()
}

func TestInstantiateVerified_ScopeValidates_AlfCoreScopePassedRejected(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	manifest := `alf_envelope_version = 1
id      = "alf-fs-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "C"

[[depends]]
handle = "alf:fs"

[depends.scope]
ignored = "value"
`
	in, _ := signBundle(t, manifest, nil)
	_, err := inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, ErrDependsScopeNonEmptyButNoSchema) {
		t.Fatalf("want ErrDependsScopeNonEmptyButNoSchema, got %v", err)
	}
}
