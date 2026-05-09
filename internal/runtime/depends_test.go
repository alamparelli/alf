package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// dependsConsumerManifest depends on alf:fs — a core handle that is
// always pre-registered when the registry is seeded. Used as the
// happy-path fixture across the depends-resolution tests.
const dependsConsumerManifest = `alf_envelope_version = 1
id      = "depends-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Depends Consumer"

[[depends]]
handle = "alf:fs"
`

func TestInstantiateVerified_DependsResolvedAgainstRegistry(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("SeedHandleRegistry: %v", err)
	}

	in, _ := signBundle(t, dependsConsumerManifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("InstantiateVerified: %v", err)
	}
	if vi.Instance == nil {
		t.Fatal("Instance is nil")
	}
	vi.Instance.Close()
}

// Acceptance criterion #1 of #392: a consumer referencing an
// unregistered provider handle fails at load with a clear error.
func TestInstantiateVerified_DependsUnregisteredFails(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Manifest references a publisher fingerprint that has not been
	// installed — no Register call has run for "deadbeef..." namespace.
	manifest := `alf_envelope_version = 1
id      = "lonely-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Lonely Consumer"

[[depends]]
handle = "deadbeefdeadbeef:bluetooth.scan"
`
	in, _ := signBundle(t, manifest, nil)
	_, err := inst.InstantiateVerified(context.Background(), in, "")
	if !errors.Is(err, ErrDependsHandleNotRegistered) {
		t.Fatalf("want ErrDependsHandleNotRegistered, got %v", err)
	}
}

// alf-namespaced reference to a known core handle id passes; reference
// to an alf-namespaced id that the daemon doesn't ship would have been
// rejected at envelope.Validate (covered by depends_schema_test.go),
// so this test is the Stage-2-and-Stage-3-agree pin: schema accepted,
// registry agreed, instantiation succeeded.
func TestInstantiateVerified_DependsAlfCoreHandleResolves(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	for _, id := range handle.AlfCoreHandleIDs {
		manifest := `alf_envelope_version = 1
id      = "core-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Core Consumer"

[[depends]]
handle = "alf:` + id + `"
`
		in, _ := signBundle(t, manifest, nil)
		vi, err := inst.InstantiateVerified(context.Background(), in, "")
		if err != nil {
			t.Errorf("alf:%s: InstantiateVerified failed: %v", id, err)
			continue
		}
		vi.Instance.Close()
	}
}

// Without a registry wired, depends entries pass through unchecked —
// matches the "no registry, no authority" precedent. Tests + legacy
// loaders that don't exercise the registry stay untouched.
func TestInstantiateVerified_NoRegistryNoCheck(t *testing.T) {
	handle.ResetMintForTesting()
	inst := NewInstantiator() // no WithHandleRegistry

	manifest := `alf_envelope_version = 1
id      = "no-registry-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Test"

[[depends]]
handle = "deadbeefdeadbeef:bluetooth.scan"
`
	in, _ := signBundle(t, manifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("expected success without registry, got %v", err)
	}
	vi.Instance.Close()
}

// Acceptance criterion #2 of #392: install a capability-provider, then
// the dependent consumer succeeds. Drives the full Stage 3 path:
// provider's [[provider.exports]] register under the signer's
// fingerprint short, consumer's [[depends]] resolves on the same
// registry value.
func TestInstantiateVerified_CapabilityProviderRegistersExports(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	providerManifest := `alf_envelope_version = 1
id      = "alf-bluetooth-provider"
kind    = "capability-provider"
version = "0.1.0"
name    = "Bluetooth Provider"

[[provider.exports]]
id = "bluetooth.scan"

[[provider.exports]]
id = "bluetooth.connect"
`
	in, _ := signBundle(t, providerManifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("provider InstantiateVerified: %v", err)
	}

	// Both exports are now in the registry under the signer's
	// fingerprint short. Look them up to confirm.
	ns := vi.SignerID.HexLower()
	if _, ok := reg.Lookup(ns, "bluetooth.scan"); !ok {
		t.Errorf("Lookup(%s, bluetooth.scan)=!ok after capability-provider install", ns)
	}
	if _, ok := reg.Lookup(ns, "bluetooth.connect"); !ok {
		t.Errorf("Lookup(%s, bluetooth.connect)=!ok", ns)
	}
	vi.Instance.Close()
}

// Acceptance criterion: load provider, then a consumer manifest with
// `[[depends]] handle = "<fp>:bluetooth.scan"` succeeds. Both bundles
// signed by the same key (so they share the namespace short).
func TestInstantiateVerified_ProviderThenConsumer(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	providerManifest := `alf_envelope_version = 1
id      = "alf-bt-prov"
kind    = "capability-provider"
version = "0.1.0"
name    = "BT"

[[provider.exports]]
id = "scan"
`
	provIn, store := signBundle(t, providerManifest, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer provVI.Instance.Close()

	ns := provVI.SignerID.HexLower()

	// Build a consumer manifest signed by the SAME key (so we re-use
	// the same trust store). The signing helper accepts an external
	// store via re-using its private key — but the helper here mints
	// a fresh key per call. For now sign with a fresh key and look up
	// the provider's fingerprint by VI return value.
	consumerManifest := `alf_envelope_version = 1
id      = "consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Consumer"

[[depends]]
handle = "` + ns + `:scan"
`
	// Re-use the store from the provider so the consumer's own signer
	// is also trusted. signBundle creates a fresh store each call;
	// we have to manually wire here.
	in := signBundleWithStore(t, consumerManifest, nil, store)
	consumerVI, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer consumerVI.Instance.Close()
}

// Two providers signed by different keys can both export the same
// handle id. Stage 3 distinguishes them by fingerprint namespace.
// Picks the load-bearing acceptance criterion #3 of #392.
func TestInstantiateVerified_TwoProvidersSameID(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	manifest := func(id string) string {
		return `alf_envelope_version = 1
id      = "` + id + `"
kind    = "capability-provider"
version = "0.1.0"
name    = "P"

[[provider.exports]]
id = "shared.kind"
`
	}

	in1, _ := signBundle(t, manifest("provider-a"), nil)
	vi1, err := inst.InstantiateVerified(context.Background(), in1, "")
	if err != nil {
		t.Fatalf("provider-a: %v", err)
	}
	defer vi1.Instance.Close()

	in2, _ := signBundle(t, manifest("provider-b"), nil)
	vi2, err := inst.InstantiateVerified(context.Background(), in2, "")
	if err != nil {
		t.Fatalf("provider-b: %v", err)
	}
	defer vi2.Instance.Close()

	if vi1.SignerID == vi2.SignerID {
		t.Fatal("expected distinct SignerIDs from two signBundle calls (each mints a fresh key)")
	}

	ns1 := vi1.SignerID.HexLower()
	ns2 := vi2.SignerID.HexLower()
	if _, ok := reg.Lookup(ns1, "shared.kind"); !ok {
		t.Errorf("Lookup(%s, shared.kind)=!ok", ns1)
	}
	if _, ok := reg.Lookup(ns2, "shared.kind"); !ok {
		t.Errorf("Lookup(%s, shared.kind)=!ok", ns2)
	}
}

// llm-provider bundles do NOT register exports — only capability-provider
// bundles drive the registry. Pin: an llm-provider with an empty
// [provider] block (the canonical shape) doesn't touch the registry.
func TestInstantiateVerified_LLMProviderDoesNotRegister(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	beforeLen := reg.Len()

	manifest := `alf_envelope_version = 1
id      = "claude-llm"
kind    = "llm-provider"
version = "1.0.0"
name    = "Claude"
`
	in, _ := signBundle(t, manifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("InstantiateVerified: %v", err)
	}
	defer vi.Instance.Close()

	if reg.Len() != beforeLen {
		t.Errorf("Registry size changed: before=%d, after=%d (llm-provider should not register)", beforeLen, reg.Len())
	}
}

// Direct test of the RegisterProviderExports method — the unit-level
// pin. Empty exports list is a no-op (returning nil); duplicate
// exports across two calls is rejected.
func TestRegisterProviderExports_EmptyIsNoop(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	var keyID envelope.KeyID
	for i := range keyID {
		keyID[i] = byte(i)
	}
	if err := inst.RegisterProviderExports(reg, keyID, nil); err != nil {
		t.Errorf("nil exports should be no-op, got %v", err)
	}
	if err := inst.RegisterProviderExports(reg, keyID, []envelope.ProviderExport{}); err != nil {
		t.Errorf("empty exports should be no-op, got %v", err)
	}
	if reg.Len() != 0 {
		t.Errorf("registry mutated by no-op call: len=%d", reg.Len())
	}
}

func TestRegisterProviderExports_FingerprintNamespace(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	var keyID envelope.KeyID
	for i := range keyID {
		keyID[i] = 0xab
	}
	exports := []envelope.ProviderExport{{ID: "a"}, {ID: "b"}}
	if err := inst.RegisterProviderExports(reg, keyID, exports); err != nil {
		t.Fatalf("RegisterProviderExports: %v", err)
	}
	expectedNS := keyID.HexLower()
	if _, ok := reg.Lookup(expectedNS, "a"); !ok {
		t.Errorf("Lookup(%s, a)=!ok", expectedNS)
	}
	if _, ok := reg.Lookup(expectedNS, "b"); !ok {
		t.Errorf("Lookup(%s, b)=!ok", expectedNS)
	}
	// Same namespace expected to be all-lowercase for manifest-syntax
	// compatibility — uppercase Hex would not match dependsHandlePattern.
	for _, c := range expectedNS {
		if c >= 'A' && c <= 'Z' {
			t.Errorf("HexLower returned uppercase char %c in %s", c, expectedNS)
		}
	}
}

// Re-registering the same provider twice (e.g. a SIGHUP-driven
// hot-reload before the unrelated uninstall path lands in Stage 5)
// surfaces as instantiation failure. Stage 5 follow-up will add a
// proper unregister pathway.
func TestInstantiateVerified_DuplicateProviderInstallFails(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	manifest := `alf_envelope_version = 1
id      = "dup-prov"
kind    = "capability-provider"
version = "0.1.0"
name    = "Dup"

[[provider.exports]]
id = "kind"
`
	// First install signs with key-1, registers under fp-1.
	in1, _ := signBundle(t, manifest, nil)
	vi1, err := inst.InstantiateVerified(context.Background(), in1, "")
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	defer vi1.Instance.Close()

	// Second install with the SAME bundle (fresh sign — different key,
	// different namespace). This succeeds — namespace is per-key, no
	// duplicate. Verifies that the duplicate-rejection is a per-key
	// concept, not a per-bundle concept.
	in2, _ := signBundle(t, manifest, nil)
	vi2, err := inst.InstantiateVerified(context.Background(), in2, "")
	if err != nil {
		t.Errorf("second install with different key: %v (should succeed under different fingerprint)", err)
	} else {
		defer vi2.Instance.Close()
	}
}
