//go:build linux

package tooling

import (
	"os/exec"
	"syscall"
)

// dropToAlfUser sets the subprocess credentials to uid/gid 1000 (alf).
// The daemon runs as alfd (uid 1001) which has vault/secret access;
// user tools must never inherit those privileges.
func dropToAlfUser(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: 1000, Gid: 1000}
}
