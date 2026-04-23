//go:build !linux

package exec

import (
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// SandboxConfig configures the isolation for a sandboxed bash execution.
type SandboxConfig struct {
	AppSlug     string
	AppDataDir  string
	Network     bool
	VaultSocket string // optional: per-app vault proxy socket path
}

// SandboxedCmd is a no-op fallback on non-Linux systems.
// Drops to uid 1000 and logs a warning — no namespace isolation available.
func SandboxedCmd(cmd *exec.Cmd, originalCommand string, cfg SandboxConfig) {
	log.Printf("[sandbox] WARNING: namespace isolation not available on this OS — falling back to uid drop only")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}
}

// SandboxSafeEnv returns a minimal environment for sandboxed processes.
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
type ServerSandboxConfig struct {
	AppSlug     string
	AppDir      string
	VaultSocket string // optional: per-app vault proxy socket path
}

// SandboxServerCmd is a no-op fallback on non-Linux systems.
func SandboxServerCmd(cmd *exec.Cmd, cfg ServerSandboxConfig) {
	log.Printf("[sandbox] WARNING: server sandbox not available on this OS — falling back to uid drop only")
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

// dnsSnippet returns the shell snippet to copy resolv.conf into the new root.
// Only included when the app has network permission.
func dnsSnippet(network bool) string {
	if network {
		return `[ -f /etc/resolv.conf ] && cp /etc/resolv.conf "$NEWROOT/etc/resolv.conf"`
	}
	return "# no network — DNS not available"
}

// shellQuote returns a POSIX shell single-quoted string.
// Strips null bytes to prevent truncation attacks (SEC-010).
func shellQuote(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
