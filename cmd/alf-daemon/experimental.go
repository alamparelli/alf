package main

import "fmt"

// experimentalBanner prints after the "starting..." line when the boot gate
// opens. Combined with the gate in requireExperimentalGate, operators cannot
// unknowingly run a 0.8.0 development build that lacks isolation.
//
// Refs: docs/ARCHITECTURE-SECURITY.md §12 safety rules, ticket #406.
const experimentalBanner = `
!! =================================================================== !!
!!  ALF_EXPERIMENTAL=1 — NO ISOLATION                                   !!
!!                                                                      !!
!!  This is a 0.8.0 development snapshot. The legacy sandbox has been   !!
!!  razed (chroot+setpriv, firewall gate, ctx-borne policy). The ocap   !!
!!  forge replacement is not in place. Do not run on shared systems.    !!
!!                                                                      !!
!!  See: docs/ARCHITECTURE-SECURITY.md §12, ticket #406                  !!
!! =================================================================== !!
`

// requireExperimentalGate returns nil when ALF_EXPERIMENTAL=1 and an error
// otherwise. Takes a getenv callback so the check is driveable from tests
// without touching process env.
//
// Callers are expected to log.Fatal on non-nil so the daemon exits non-zero
// at first responsibility: main() before any other init step.
func requireExperimentalGate(getenv func(string) string) error {
	if getenv("ALF_EXPERIMENTAL") == "1" {
		return nil
	}
	return fmt.Errorf("alf-daemon refuses to boot: set ALF_EXPERIMENTAL=1 to acknowledge the 0.8.0 development window has no sandbox isolation (see ticket #406 and docs/ARCHITECTURE-SECURITY.md §12)")
}
