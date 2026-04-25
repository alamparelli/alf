// Archtest rules for #395 — pin the §6 admin boundary.
//
// The principle: any code path that grows the system's trust surface
// (add a key, sign a bundle, ratify a pending action, unlock the
// user-scope vault) lives under internal/admin/ and must be reachable
// only from TTY-direct CLI commands or the dedicated CC admin trust
// domain. No capability, no Runtime tool dispatch, no LLM-driven
// route may reach into this subtree.
//
// If a future change legitimately needs admin-side access from a new
// surface, the right move is to add the surface to allowedAdminConsumers
// with a one-line justification — the same pattern deps_test.go uses
// for leaf-foundation exceptions.
package archtest_test

import (
	"strings"
	"testing"
)

// allowedAdminConsumers is the closed set of packages that may import
// internal/admin/* . Adding to this list is a deliberate architectural
// decision and should be reviewed like any other PR change.
var allowedAdminConsumers = map[string]string{
	"cmd/alf":           "alf trust / pending / ratify CLI commands run TTY-direct",
	"cmd/alf-daemon":    "daemon-side append: Runtime enqueues a pending item when a cap reaches a ratification-required point",
	"internal/cli":      "shared CLI helpers used by cmd/alf admin commands",
	"internal/admin":    "package-internal cross-references between admin sub-packages",
}

// TestAdminPackageBoundary enforces §6 by walking every package and
// flagging any import of internal/admin/* from outside the allowlist.
//
// This is the load-bearing pin for the admin trust domain: if a
// capability or Runtime path can reach internal/admin/, the LLM can
// drive trust-modifying operations through prompt injection. The
// archtest fails the build before that path lands.
func TestAdminPackageBoundary(t *testing.T) {
	pkgs, err := listPackages(t)
	if err != nil {
		t.Skipf("archtest skipped: go list failed: %v", err)
		return
	}

	const adminPrefix = "internal/admin"

	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePrefix)
		// Skip anything inside the admin subtree (package-internal
		// imports are fine).
		if rel == adminPrefix || strings.HasPrefix(rel, adminPrefix+"/") {
			continue
		}
		// Skip non-internal packages — pkg/ can import admin/ if it
		// later exposes a public surface. Right now no pkg/ does.
		if !strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "cmd/") {
			continue
		}

		consumerRoot := topLevelConsumer(rel)
		justification, allowed := allowedAdminConsumers[consumerRoot]

		for _, imp := range p.Imports {
			impRel := strings.TrimPrefix(imp, modulePrefix)
			if !strings.HasPrefix(impRel, adminPrefix+"/") && impRel != adminPrefix {
				continue
			}
			if !allowed {
				t.Errorf("ADMIN BOUNDARY VIOLATION: %s imports %s. "+
					"Per ARCHITECTURE-SECURITY §6 + #395, the admin trust domain is reachable "+
					"only from TTY-direct CLI commands and the dedicated CC admin trust domain. "+
					"If this consumer legitimately needs admin-side access, add it to "+
					"allowedAdminConsumers with a one-line justification.",
					rel, impRel)
				continue
			}
			t.Logf("ALLOWED: %s imports %s — %s", rel, impRel, justification)
		}
	}
}
