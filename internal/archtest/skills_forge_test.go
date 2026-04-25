// Archtest rules for #389 — pin the §3.1 + §7.10 invariants the
// skill loader depends on:
//
//  1. Every manifest.toml shipped under skills.d/* must validate
//     against the 0.8.0 envelope schema. A new shipped skill that
//     introduces a malformed manifest, an unknown field, or a
//     deferred block (per §3.4) fails the build before it can land.
//  2. Every shipped skill's manifest declares kind = "skill" — never
//     "wasm-tool" or other kinds. Cross-kind drift would silently
//     break the LoadDir / Instantiator path, which assumes skill
//     bundles always carry SKILL.md.
//
// These rules complement the existing #388 / #391 / #400 archtests
// already pinning the verify call site, the runtime-token forge
// gate, and the absence of MemoryHandle. They activate as soon as
// shipped skills migrate to manifest.toml in Étape 7.
package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// TestShippedSkillManifestsValidate walks the repo's skills.d/ tree
// and runs envelope.Validate on every manifest.toml found. The
// validator catches schema drift, unknown fields, deferred blocks,
// malformed IDs, and any other condition the runtime would reject at
// load. Catching this in CI keeps the migration honest.
//
// Skill subdirs without a manifest.toml are skipped — they remain on
// the legacy YAML-only path until Étape 8 closes that window.
func TestShippedSkillManifestsValidate(t *testing.T) {
	root := repoRoot()
	skillsDir := filepath.Join(root, "skills.d")

	if _, err := os.Stat(skillsDir); err != nil {
		t.Skipf("skills.d not found: %v", err)
		return
	}

	walkErr := filepath.Walk(skillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) != "manifest.toml" {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			return nil
		}

		m, err := envelope.Validate(raw)
		if err != nil {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("envelope.Validate failed for %s: %v\n"+
				"Every shipped manifest must satisfy the 0.8.0 schema (see docs/MANIFEST-SCHEMA.md).",
				rel, err)
			return nil
		}

		// Skill bundles always carry SKILL.md — the loader treats it
		// as the bundle for hash pinning. Cross-kind drift would
		// break that contract silently.
		if !strings.HasSuffix(path, string(filepath.Separator)+"manifest.toml") {
			return nil
		}
		// Only enforce kind == skill for files under skills.d/<name>/
		// (i.e. the top-level skill bundles). Nested manifests under
		// skills.d/wasm/ are wasm-tool bundles by design and have
		// their own archtests.
		rel, _ := filepath.Rel(skillsDir, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 2 && parts[0] == "wasm" {
			return nil // wasm/ subtree exempt
		}

		if m.Kind != envelope.KindSkill {
			relRoot, _ := filepath.Rel(root, path)
			t.Errorf("%s: manifest.kind=%q, want %q\n"+
				"Skill bundles under skills.d/ (outside skills.d/wasm/) must declare kind = \"skill\".",
				relRoot, m.Kind, envelope.KindSkill)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk skills.d: %v", walkErr)
	}
}
