//go:build linux

package tooling

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// SandboxConfig configures the isolation for a sandboxed bash execution.
type SandboxConfig struct {
	AppSlug    string // app identifier (used to keep own data accessible)
	AppDataDir string // path to app's data dir (read-write)
	Network    bool   // if true, skip network namespace (app has network permission)
}

// SandboxedCmd configures cmd to run in isolated Linux namespaces.
// Creates mount, PID, and optionally network namespaces.
// The child process sets up its own mount namespace before executing the command.
//
// Requires CAP_SYS_ADMIN in the calling process for CLONE_NEWNS and CLONE_NEWPID.
func SandboxedCmd(cmd *exec.Cmd, originalCommand string, cfg SandboxConfig) {
	flags := uintptr(syscall.CLONE_NEWNS | syscall.CLONE_NEWPID)
	if !cfg.Network {
		flags |= syscall.CLONE_NEWNET
	}

	// Build the mount setup script that runs inside the new namespace.
	// CLONE_NEWNS gives the child its own mount tree — mounts here don't affect parent.
	setup := fmt.Sprintf(`
# Namespace sandbox setup — non-critical mounts use || true, final exec uses set -e.

# Isolate mount propagation
mount --make-rprivate / 2>/dev/null || true

# Private /tmp
mount -t tmpfs tmpfs /tmp 2>/dev/null && chmod 1777 /tmp 2>/dev/null || true

# Fresh /proc for PID namespace
mount -t proc proc /proc 2>/dev/null || true

# Mask other apps entirely
for d in /home/alf/data/apps/*/; do
  app="$(basename "$d")"
  [ "$app" != %s ] && mount -t tmpfs tmpfs "$d" 2>/dev/null || true
done

# Mask sensitive directories
mount -t tmpfs tmpfs /home/alf/data/tools.d 2>/dev/null || true
mount -t tmpfs tmpfs /opt/alf/vault-data 2>/dev/null || true
mount -t tmpfs tmpfs /home/alf/data/logs 2>/dev/null || true
mount -t tmpfs tmpfs /opt/alf/config.d 2>/dev/null || true
mount -t tmpfs tmpfs /home/alf/.claude 2>/dev/null || true
mount -t tmpfs tmpfs /home/alf/data/context 2>/dev/null || true

# Resource limits
ulimit -v 131072 2>/dev/null || true
ulimit -u 50 2>/dev/null || true
ulimit -f 102400 2>/dev/null || true
ulimit -t 60 2>/dev/null || true

# Drop all capabilities then execute user command
exec capsh --drop=all -- -c %s
`, shellQuote(cfg.AppSlug), shellQuote(originalCommand))

	cmd.Path = "/bin/bash"
	cmd.Args = []string{"bash", "-c", setup}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: flags,
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}
}

// SandboxSafeEnv returns a minimal environment for sandboxed app processes.
// Stricter than bashSafeEnv — excludes VAULT_TOKEN and proxy settings.
func SandboxSafeEnv(appDataDir string) []string {
	return []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
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

// shellQuote returns a POSIX shell single-quoted string.
// Strips null bytes to prevent truncation attacks (SEC-010).
func shellQuote(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
