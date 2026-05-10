package main

import (
	"log"
	"strings"
)

// truthyExperimentalValues are the env values an operator might use to
// "set" the deprecated flag. Anything else (empty, "0", "false", "off",
// "no") is interpreted as "explicitly disabled" — the daemon stays
// silent because the operator has clearly already removed the flag in
// spirit, even if the line is still present in their docker-compose.yml.
// SEC-080-010: the previous implementation warned for any non-empty
// value, including ALF_EXPERIMENTAL=0, which surprised operators who
// had downgraded the value rather than deleting the line.
var truthyExperimentalValues = map[string]bool{
	"1":    true,
	"true": true,
	"yes":  true,
	"on":   true,
	"y":    true,
	"t":    true,
}

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
// ALF_EXPERIMENTAL is set to a truthy value in the environment. The
// flag is a no-op as of v0.8.0 final but operators may still have it
// in their docker-compose.yml from the dev window. Pointing at the
// removal step keeps the migration friction low.
//
// SEC-080-010: only truthy values trigger the warning. ALF_EXPERIMENTAL=0
// (or "false", "off", etc.) is treated the same as unset — the operator
// has already disabled it semantically and doesn't need a noisier
// reminder to clean up the now-meaningless line.
func warnDeprecatedExperimentalEnv(getenv func(string) string) {
	v := strings.ToLower(strings.TrimSpace(getenv("ALF_EXPERIMENTAL")))
	if !truthyExperimentalValues[v] {
		return
	}
	log.Printf("[boot] DEPRECATED: ALF_EXPERIMENTAL=%q is set but no longer used. "+
		"The 0.8.0 dev window has closed; the daemon now boots into strict ocap "+
		"by default. Remove the variable from your docker-compose.yml — kept here "+
		"as a no-op until the next minor release.", v)
}
