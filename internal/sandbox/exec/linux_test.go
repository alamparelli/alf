//go:build linux

package exec

import "testing"

// 0.8.0-demo: the Linux chroot+setpriv+bwrap implementation was razed in
// #406. The tests that used to live here asserted the contents of the bash
// script the old SandboxedCmd assembled — a script that no longer exists.
// Stubbed wholesale rather than deleted so the build tag + package anchor
// stay, and so the git history points grep'ers at this explanation. The
// original assertions (chroot present, setpriv drops caps, CLONE_NEWNET on
// Network=false, …) must be re-established under #391 (ocap forge, handle
// scope enforcement) and #86 (Layer 1 outer ring — AppArmor + seccomp).

func TestSandboxedCmd_ScriptContent_RazedIn406(t *testing.T) {
	t.Skip("0.8.0-demo: razed in #406; ocap forge (#391) + L1 outer ring (#86) replace process sandbox")
}
