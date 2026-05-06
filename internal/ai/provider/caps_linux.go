//go:build linux

package provider

import "syscall"

// dropAmbientCaps is intentionally a no-op now — see capDropWrap below.
//
// Earlier attempt set spa.AmbientCaps = []uintptr{} hoping to trigger
// PR_CAP_AMBIENT_CLEAR_ALL in Go's syscall.ForkExec. That doesn't work:
// stdlib gates the prctl block on `len(sys.AmbientCaps) != 0`, so an
// empty slice is treated identically to nil (no-op). Since the daemon
// can't both clear ambient AND raise nothing via this field, we drop
// caps via setpriv at exec time instead — see capDropWrap.
//
// Kept as a placeholder so a future Go release that exposes a real
// "clear ambient" knob can land here without renaming call sites.
func dropAmbientCaps(spa *syscall.SysProcAttr) { _ = spa }

// capDropWrap returns the argv that runs `name args...` with all
// ambient and inheritable caps cleared at exec time. The chain is:
//
//	setpriv --ambient-caps=-all --inh-caps=-all -- <name> <args...>
//
// PR_CAP_AMBIENT_CLEAR_ALL requires no privilege — the kernel always
// permits a thread to drop its own ambient caps. setpriv is in the
// runtime image (used by entrypoint.sh phase 3) and exits with the
// child's exit code, so the wrap is transparent.
//
// 0.8.0-beta soak finding: the daemon (alfd) inherits ambient
// CAP_SETUID + CAP_SETGID from entrypoint.sh phase 3 because it
// needs them to spawn LLM subprocesses as user alf via
// SysProcAttr.Credential. Without explicit cap-drop those propagate
// to the LLM child, letting it call setfsuid()/setresuid() and read
// files owned by alfd (daemon signing key, vault socket cookie,
// cc_auth_token) that should be DAC-blocked from uid 1000.
//
// Combined with #395 §6 admin boundary: nothing the LLM can drive
// should reach the trust surface. This is the spawn-level
// enforcement that was missing pre-beta.
func capDropWrap(name string, args []string) (string, []string) {
	full := append([]string{"--ambient-caps=-all", "--inh-caps=-all", "--", name}, args...)
	return "setpriv", full
}
