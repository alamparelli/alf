package exec

import (
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// 0.8.0-demo: the Linux-specific chroot + setpriv + bwrap implementation was
// razed in #406 per the 0.7.9 security audit §13 (chroot escape,
// CAP_SYS_ADMIN, apparmor=unconfined). Every function in this file now
// behaves like the old non-Linux fallback did: uid/gid drop only, no
// namespace isolation. That is deliberate — the ocap forge (#391) will
// replace process-level isolation with capability-scoped handles. Until
// then, the daemon refuses to boot without ALF_EXPERIMENTAL=1 and each HTTP
// response is tagged X-ALF-Experimental: no-isolation.
//
// Path helpers (ResolvePath, CheckBoundary) continue to live in path.go.
// They do workspace containment — orthogonal to process isolation — and
// remain useful under ocap.

// SandboxConfig configures the isolation for a sandboxed bash execution.
// In the dev window its fields are accepted for call-site compatibility but
// not enforced. The forge work (#391) will replace this struct with a
// handle-scope derivation.
type SandboxConfig struct {
	AppSlug     string
	AppDataDir  string
	Network     bool
	VaultSocket string // optional: per-app vault proxy socket path
}

// SandboxedCmd attaches a best-effort credential drop (uid 1000) to cmd and
// logs that isolation is disabled for the 0.8.0 dev window. originalCommand
// is accepted for signature compatibility but no longer wrapped in a
// chroot/bwrap script.
func SandboxedCmd(cmd *exec.Cmd, originalCommand string, cfg SandboxConfig) {
	log.Printf("[sandbox] experimental: namespace isolation razed — uid drop only (ticket #406)")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}
}

// SandboxSafeEnv returns a minimal environment for sandboxed processes.
// Retained because the curated env is still useful as a hygiene default
// even without namespace isolation.
func SandboxSafeEnv(appDataDir string) []string {
	return []string{
		"PATH=/opt/alf/tools.d:/usr/local/bin:/usr/bin:/bin",
		"HOME=/home/alf",
		"USER=alf",
		"LOGNAME=alf",
		"SHELL=/bin/bash",
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"ALF_APP_DATA_DIR=" + appDataDir,
		"TMPDIR=/tmp",
	}
}

// ServerSandboxConfig configures isolation for a long-running app server.
// Same dev-window contract as SandboxConfig.
type ServerSandboxConfig struct {
	AppSlug     string
	AppDir      string
	VaultSocket string // optional: per-app vault proxy socket path
}

// SandboxServerCmd is the long-running-server counterpart to SandboxedCmd.
// Best-effort uid drop only in the dev window.
func SandboxServerCmd(cmd *exec.Cmd, cfg ServerSandboxConfig) {
	log.Printf("[sandbox] experimental: server isolation razed — uid drop only (ticket #406)")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}
}

// ServerSafeEnv returns a minimal environment for sandboxed server processes.
func ServerSafeEnv(appDir string) []string {
	return []string{
		"PATH=/opt/alf/tools.d:/usr/local/bin:/usr/bin:/bin",
		"HOME=/home/alf",
		"USER=alf",
		"LOGNAME=alf",
		"SHELL=/bin/bash",
		"LANG=en_US.UTF-8",
		"ALF_APP_DATA_DIR=" + filepath.Join(appDir, "data"),
		"TMPDIR=/tmp",
	}
}

// shellQuote returns a POSIX shell single-quoted string. Retained after the
// bwrap script razing in #406 because it is a SEC-010 defensive helper
// (null-byte strip + single-quote escape), not a sandbox mechanism. The
// tests in path_test.go are the regression guard for that CVE pattern.
func shellQuote(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

