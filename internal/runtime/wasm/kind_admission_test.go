package wasm

import (
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// TestIsLoaderAdmittedKind_Allowlist pins the §4.1 lockdown (#420):
// the on-disk WASM loader admits wasm-tool, wasm-app, skill,
// llm-provider, capability-provider. It refuses marketplace-app
// (retired per MANIFEST-SCHEMA §3.3) and any unknown kind.
//
// Drift here would re-introduce a non-WASM on-disk path and reopen
// the asymmetry the doctrine forbids — covered by an archtest
// invariant in TestWasmLoaderKindAllowlistMatchesDoctrine.
func TestIsLoaderAdmittedKind_Allowlist(t *testing.T) {
	cases := []struct {
		kind envelope.ManifestKind
		want bool
	}{
		{envelope.KindWASMTool, true},
		{envelope.KindWASMApp, true},
		{envelope.KindSkill, true},
		{envelope.KindLLMProvider, true},
		{envelope.KindCapabilityProvider, true},
		{envelope.KindMarketplaceApp, false},
		{envelope.ManifestKind("go-app"), false},
		{envelope.ManifestKind("bash-tool"), false},
		{envelope.ManifestKind(""), false},
		{envelope.ManifestKind("nonsense"), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			if got := IsLoaderAdmittedKind(tc.kind); got != tc.want {
				t.Fatalf("IsLoaderAdmittedKind(%q) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

// TestCheckKindAdmission_RejectsForbidden ensures the error returned
// by the loader carries ErrKindForbiddenByLoader so callers can match
// it with errors.Is and emit the right operator log line.
func TestCheckKindAdmission_RejectsForbidden(t *testing.T) {
	m := &envelope.Manifest{Kind: envelope.KindMarketplaceApp}
	err := checkKindAdmission(m)
	if err == nil {
		t.Fatalf("checkKindAdmission: expected error for marketplace-app, got nil")
	}
	if !errors.Is(err, ErrKindForbiddenByLoader) {
		t.Fatalf("checkKindAdmission: expected ErrKindForbiddenByLoader, got %v", err)
	}
}

// TestCheckKindAdmission_AdmitsWASMKinds pins the happy path —
// otherwise we'd risk a regression that locks out all kinds.
func TestCheckKindAdmission_AdmitsWASMKinds(t *testing.T) {
	for _, k := range []envelope.ManifestKind{
		envelope.KindWASMTool,
		envelope.KindWASMApp,
		envelope.KindSkill,
		envelope.KindLLMProvider,
		envelope.KindCapabilityProvider,
	} {
		t.Run(string(k), func(t *testing.T) {
			if err := checkKindAdmission(&envelope.Manifest{Kind: k}); err != nil {
				t.Fatalf("checkKindAdmission(%q): %v", k, err)
			}
		})
	}
}

// TestCheckKindAdmission_NilManifest guards the defensive nil branch
// so a future refactor that hands nil to the gate doesn't crash the
// loader with a NPE.
func TestCheckKindAdmission_NilManifest(t *testing.T) {
	err := checkKindAdmission(nil)
	if err == nil {
		t.Fatalf("checkKindAdmission(nil): expected error, got nil")
	}
	if !errors.Is(err, ErrKindForbiddenByLoader) {
		t.Fatalf("checkKindAdmission(nil): expected ErrKindForbiddenByLoader, got %v", err)
	}
}
