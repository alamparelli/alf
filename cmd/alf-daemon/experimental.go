package main

import "log"

// The 0.8.0 development-window gate (`ALF_EXPERIMENTAL=1` + the
// multi-line "NO ISOLATION" banner + the `X-ALF-Experimental`
// CC response header) was retired at v0.8.0 final. The Layer 1
// outer ring (#86), Layer 2 trust (#388 + #387 + #397), and
// Layer 3 ocap forge (#391 + #392 + #399 + #400) are all in
// place; the daemon now boots into the strict ocap posture by
// default. Operators no longer need to set any flag.
//
// We keep the file (rather than deleting it) so the deprecation
// helper below can WARN — but not refuse boot — for operators
// who left `ALF_EXPERIMENTAL=1` in their docker-compose.yml from
// the dev window. The warning fires once at boot, suggests the
// removal, and the daemon proceeds normally.
//
// Refs: docs/ARCHITECTURE-SECURITY.md §12 final-tag transition;
// commits closing #86 + #396 + #392 + the strict-flip itself.

// warnDeprecatedExperimentalEnv emits one log line at boot when
// ALF_EXPERIMENTAL=1 is still set in the environment. The flag is
// no-op as of v0.8.0 final but operators may still have it in
// their docker-compose.yml from the dev window. Pointing at the
// removal step keeps the migration friction low.
func warnDeprecatedExperimentalEnv(getenv func(string) string) {
	if getenv("ALF_EXPERIMENTAL") == "" {
		return
	}
	log.Printf("[boot] DEPRECATED: ALF_EXPERIMENTAL is set but no longer used. " +
		"The 0.8.0 dev window has closed; the daemon now boots into strict ocap " +
		"by default. Remove the variable from your docker-compose.yml — kept here " +
		"as a no-op until the next minor release.")
}
