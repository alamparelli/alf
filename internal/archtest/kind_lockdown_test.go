// Archtest rules for #420 — pin the §4.1 structural lockdown:
// non-WASM kinds are unreachable from on-disk loaders, and the
// retired "marketplace-app" kind is referenced only by the legacy
// shims that exist for compatibility.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/runtime/wasm"
)

// TestWasmLoaderKindAllowlistMatchesDoctrine pins the structural
// admission rule from ARCHITECTURE-SECURITY.md §4.1: the on-disk
// WASM loader admits exactly {wasm-tool, wasm-app, skill,
// llm-provider, capability-provider}. Adding a new kind to
// envelope.ManifestKind without updating the loader's allowlist
// fails this test — the operator gets a CI signal before the
// daemon silently regains an asymmetric admission path.
//
// Drift here is precisely what the issue forbids: a future "Go-kind
// from disk" would skip wazero's wall and the Tier 3.1 forge guards.
// The test consumes the loader's exported predicate so the source
// of truth stays in one place.
func TestWasmLoaderKindAllowlistMatchesDoctrine(t *testing.T) {
	admitted := map[envelope.ManifestKind]bool{
		envelope.KindWASMTool:           true,
		envelope.KindWASMApp:            true,
		envelope.KindSkill:              true,
		envelope.KindLLMProvider:        true,
		envelope.KindCapabilityProvider: true,
		envelope.KindMarketplaceApp:     false,
	}

	for k, want := range admitted {
		got := wasm.IsLoaderAdmittedKind(k)
		if got != want {
			t.Errorf("wasm.IsLoaderAdmittedKind(%q) = %v, want %v\n"+
				"Doctrine: ARCHITECTURE-SECURITY.md §4.1 — on-disk loader admits only WASM kinds + skill + provider variants.\n"+
				"If you intend to add %q to the loader, update both:\n"+
				"  - internal/runtime/wasm/kind_admission.go loaderAdmittedKinds\n"+
				"  - this archtest's expected map", k, got, want, k)
		}
	}
}

// TestMarketplaceAppKindOnlyInLegacyShims pins that envelope.KindMarketplaceApp
// is only referenced by the bounded set of legacy shims kept for
// compatibility and migration tooling. New code paths must use
// envelope.KindWASMApp.
//
// Allowed references (per MANIFEST-SCHEMA.md §3.3 retirement + #420):
//   - internal/capability/envelope/types.go    — the deprecated constant declaration
//   - internal/capability/envelope/schema.go   — parser allowlist (accepts the value for legacy fixtures)
//   - cmd/alf/admin/keysign.go                 — admin CLI still able to detect the artefact for migration signing
//   - internal/runtime/instantiator_verified.go — legacy shim case (defensive; unreachable after loader gate)
//   - internal/runtime/wasm/kind_admission.go  — the lockdown gate itself, names the retired kind in its rationale
//   - tests (*_test.go) — fixtures that exercise rejection paths
//
// Any other production .go file referencing KindMarketplaceApp is a
// new entry point for the retired kind and must be cleaned.
func TestMarketplaceAppKindOnlyInLegacyShims(t *testing.T) {
	root := repoRoot()
	allowed := map[string]struct{}{
		filepath.Join("internal", "capability", "envelope", "types.go"):   {},
		filepath.Join("internal", "capability", "envelope", "schema.go"):  {},
		filepath.Join("cmd", "alf", "admin", "keysign.go"):                {},
		filepath.Join("internal", "runtime", "instantiator_verified.go"): {},
		filepath.Join("internal", "runtime", "wasm", "kind_admission.go"): {},
	}

	pat := regexp.MustCompile(`\bKindMarketplaceApp\b`)

	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipOcapDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Test files exercise both the kind and its rejection — they
		// are legitimate references.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := allowed[rel]; ok {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if pat.MatchString(line) {
				violations = append(violations, formatViolation(rel, i+1, line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, v := range violations {
		t.Errorf("KindMarketplaceApp referenced outside legacy shims: %s\n"+
			"Per MANIFEST-SCHEMA.md §3.3 + #420, marketplace-app is retired. "+
			"New code must use envelope.KindWASMApp.", v)
	}
}
