//go:build !linux

package tooling

import (
	"log"
	"os/exec"
	"syscall"
)

// SandboxConfig configures the isolation for a sandboxed bash execution.
type SandboxConfig struct {
	AppSlug    string
	AppDataDir string
	Network    bool
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
