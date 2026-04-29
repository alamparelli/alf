// Archtest rules for #395 Stage 2 — pin the cmd/alf/admin/ surface.
//
// Per ARCHITECTURE-SECURITY §6, the trust-mutating CLI commands
// (alf trust add/list/remove/revoke and the keygen/sign/pending
// /ratify commands landing in subsequent #395 chunks) must be
// reachable only from the alf CLI binary's main and from package-
// internal cross-references. Two complementary invariants pin this:
//
//   1. NOTHING outside cmd/alf/* may import cmd/alf/admin/*. If a
//      capability or daemon HTTP route can reach the admin package,
//      prompt injection on the daemon can drive trust-modifying
//      operations through that path.
//
//   2. cmd/alf/admin/* itself must NOT import internal/runtime/*,
//      internal/tooling/*, or internal/capability/handle/*. The
//      admin CLI is meant to mutate on-disk state directly via the
//      narrow envelope.DirTrustStore + (later) vault user-scope
//      APIs. Pulling in a tool registry or a runtime would re-link
//      the admin path to the LLM-driven dispatch surface — that's
//      the exact composition this boundary forbids.
//
// Adding a new admin sub-package is supported automatically (the
// glob covers cmd/alf/admin/...). Adding a new consumer of admin/
// requires updating allowedAdminCLIConsumers below with a one-line
// justification.
package archtest_test

import (
	"strings"
	"testing"
)

// allowedAdminCLIConsumers is the closed set of packages that may
// import cmd/alf/admin/* . The set is intentionally tiny; expanding
// it is a deliberate architectural decision and should be reviewed
// like any other PR change.
var allowedAdminCLIConsumers = map[string]string{
	"cmd/alf":           "alf trust / pending / ratify subcommands dispatched from main",
	"cmd/alf/admin":     "package-internal cross-references (admin sub-packages)",
}

// adminCLIForbiddenImports lists package prefixes the admin CLI must
// never reach. These are the LLM-driven dispatch surfaces — the
// premise of §6 is that the admin path is a separate trust domain.
var adminCLIForbiddenImports = []string{
	"internal/runtime",
	"internal/tooling",
	"internal/capability/handle",
}

// TestAdminCLIPackageBoundary enforces invariant (1): no package
// outside cmd/alf/* imports cmd/alf/admin/*.
func TestAdminCLIPackageBoundary(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	const adminCLIPrefix = "cmd/alf/admin"

	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePrefix)
		// Skip the admin subtree itself — package-internal imports
		// between admin sub-packages are fine.
		if rel == adminCLIPrefix || strings.HasPrefix(rel, adminCLIPrefix+"/") {
			continue
		}

		consumerRoot := adminConsumerRoot(rel)
		justification, allowed := allowedAdminCLIConsumers[consumerRoot]

		for _, imp := range p.Imports {
			impRel := strings.TrimPrefix(imp, modulePrefix)
			if !strings.HasPrefix(impRel, adminCLIPrefix+"/") && impRel != adminCLIPrefix {
				continue
			}
			if !allowed {
				t.Errorf("ADMIN CLI BOUNDARY VIOLATION: %s imports %s. "+
					"Per ARCHITECTURE-SECURITY §6 + #395 Stage 2, cmd/alf/admin/ is reachable "+
					"only from cmd/alf/* . If this consumer legitimately needs admin-side "+
					"access, add it to allowedAdminCLIConsumers with a one-line justification.",
					rel, impRel)
				continue
			}
			t.Logf("ALLOWED: %s imports %s — %s", rel, impRel, justification)
		}
	}
}

// TestAdminCLIDoesNotImportRuntime enforces invariant (2): the admin
// CLI subtree does not pull in any LLM-driven dispatch surface.
func TestAdminCLIDoesNotImportRuntime(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	const adminCLIPrefix = "cmd/alf/admin"

	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePrefix)
		if rel != adminCLIPrefix && !strings.HasPrefix(rel, adminCLIPrefix+"/") {
			continue
		}
		for _, imp := range p.Imports {
			impRel := strings.TrimPrefix(imp, modulePrefix)
			for _, forbidden := range adminCLIForbiddenImports {
				if impRel == forbidden || strings.HasPrefix(impRel, forbidden+"/") {
					t.Errorf("ADMIN CLI COMPOSITION VIOLATION: %s imports %s. "+
						"Per ARCHITECTURE-SECURITY §6 + #395 Stage 2, the admin CLI must "+
						"mutate on-disk state directly via narrow envelope APIs — pulling "+
						"in a runtime/tooling/handle package re-links the admin path to the "+
						"LLM-driven dispatch surface. Refactor the admin command to use a "+
						"narrower API or split the runtime side of the operation into the daemon.",
						rel, impRel)
				}
			}
		}
	}
}

// adminConsumerRoot maps a nested package path to the consumer key
// used in the allowlist. cmd/alf/admin/foo → cmd/alf/admin (for the
// package-internal exception); cmd/alf → cmd/alf; everything else
// falls back to the deps_test.go helper for internal/* paths.
func adminConsumerRoot(rel string) string {
	switch {
	case rel == "cmd/alf/admin" || strings.HasPrefix(rel, "cmd/alf/admin/"):
		return "cmd/alf/admin"
	case rel == "cmd/alf" || strings.HasPrefix(rel, "cmd/alf/"):
		return "cmd/alf"
	default:
		return topLevelConsumer(rel)
	}
}
