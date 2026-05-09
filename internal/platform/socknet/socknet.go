// Package socknet centralises Unix-socket creation with secure
// default permissions. Without it, the canonical sequence
//
//	ln, _ := net.Listen("unix", path)
//	os.Chmod(path, 0660)
//
// has a TOCTOU window between Listen and Chmod where the socket
// is reachable at the kernel-default mode (umask 002 → 0775),
// allowing any local process whose uid is not in the daemon's
// group to connect, attempt the protocol handshake, and observe
// what the protocol returns to malformed clients.
//
// SEC-407-002 (#86 sibling): every Unix socket the daemon
// publishes (signal, scheduler, memory, tools, vault) goes
// through ListenUnix0660 below so the umask-narrowed Listen
// produces a 0660 inode from byte zero.
package socknet

import (
	"net"
	"os"
	"sync"
	"syscall"
)

// listenMu serialises the umask wrapper so concurrent ListenUnix
// calls cannot stomp on each other's umask state. Go's syscall.Umask
// is process-global; without serialisation, two callers racing on
// it could leave the umask in an unexpected state for the next
// Listen.
var listenMu sync.Mutex

// ListenUnix0660 creates a Unix socket whose inode is mode 0660
// from the moment net.Listen returns. The caller chgrp's the
// socket to its target group afterwards (typically alf, gid 1000)
// so daemon + LLM subprocess share access while everyone else
// remains locked out.
//
// The umask 0o117 narrows world-rwx + group-x off (the kernel
// allocates 0o666 by default for sockets; mask leaves 0o660).
// Restored before returning so unrelated daemon-process file
// creations are not affected.
//
// Errors from os.Chown or os.Chmod after the listen are NOT
// returned: the socket is already in 0660 from creation; chgrp
// is best-effort (in tests we don't have CAP_CHOWN).
func ListenUnix0660(path string, gid int) (net.Listener, error) {
	listenMu.Lock()
	old := syscall.Umask(0o117)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	listenMu.Unlock()
	if err != nil {
		return nil, err
	}
	// Best-effort chgrp + chmod — the listen window already
	// produced a 0660 socket; these are belt-and-braces. Errors
	// are non-fatal so tests on hosts that lack CAP_CHOWN still
	// pass.
	_ = os.Chown(path, -1, gid)
	_ = os.Chmod(path, 0o660)
	return ln, nil
}
