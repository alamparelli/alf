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
	setup := fmt.Sprintf(`set -e
# Isolate mount propagation so our mounts stay private
mount --make-rprivate / 2>/dev/null || true

# Private /tmp (50MB tmpfs)
mount -t tmpfs tmpfs /tmp -o size=50m,mode=1777,nosuid,nodev

# Fresh /proc for PID namespace
mount -t proc proc /proc

# Mask other apps' data directories with empty tmpfs
for d in /home/alf/data/apps/*/data; do
  app="$(basename "$(dirname "$d")")"
  if [ "$app" != %s ]; then
    mount -t tmpfs -o size=0,ro tmpfs "$d" 2>/dev/null || true
  fi
done

# Mask vault secrets
mount -t tmpfs -o size=0,ro tmpfs /opt/alf/vault-data 2>/dev/null || true

# Execute the actual command
exec /bin/bash -c %s
`, shellQuote(cfg.AppSlug), shellQuote(originalCommand))

	cmd.Path = "/bin/bash"
	cmd.Args = []string{"bash", "-c", setup}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: flags,
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}

	// Resource limits
	cmd.SysProcAttr.Rlimit = []syscall.Rlimit{
		{Type: syscall.RLIMIT_AS, Cur: 512 << 20, Max: 512 << 20},    // 512MB virtual memory
		{Type: syscall.RLIMIT_NPROC, Cur: 50, Max: 50},               // 50 processes max
		{Type: syscall.RLIMIT_FSIZE, Cur: 100 << 20, Max: 100 << 20}, // 100MB max file size
		{Type: syscall.RLIMIT_CPU, Cur: 60, Max: 60},                 // 60s CPU time
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
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
