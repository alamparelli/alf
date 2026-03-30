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
// The child process uses pivot_root to build an allowlist filesystem:
// only explicitly bind-mounted paths are visible to the sandboxed app.
//
// Requires CAP_SYS_ADMIN in the calling process for CLONE_NEWNS and CLONE_NEWPID.
// Requires apparmor=unconfined on the Docker container (AppArmor blocks mount(2)).
func SandboxedCmd(cmd *exec.Cmd, originalCommand string, cfg SandboxConfig) {
	flags := uintptr(syscall.CLONE_NEWNS | syscall.CLONE_NEWPID)
	if !cfg.Network {
		flags |= syscall.CLONE_NEWNET
	}

	// Build the pivot_root setup script that runs inside the new namespace.
	// Instead of masking paths (blocklist), we build a minimal root with only
	// what the app needs (allowlist). Everything else simply doesn't exist.
	setup := fmt.Sprintf(`
set -e

# --- Phase 1: Create new root on tmpfs ---
NEWROOT=$(mktemp -d /tmp/sandbox-XXXXXX)
mount -t tmpfs -o size=64m tmpfs "$NEWROOT"

# --- Phase 2: Bind-mount only what the app needs ---

# System binaries (read-only)
for d in bin usr lib sbin; do
  if [ -d "/$d" ]; then
    mkdir -p "$NEWROOT/$d"
    mount --rbind "/$d" "$NEWROOT/$d"
    mount -o remount,ro,bind "$NEWROOT/$d" || { echo "FATAL: cannot make /$d read-only"; exit 1; }
  fi
done
# lib64 (exists on amd64)
if [ -d /lib64 ]; then
  mkdir -p "$NEWROOT/lib64"
  mount --rbind /lib64 "$NEWROOT/lib64"
  mount -o remount,ro,bind "$NEWROOT/lib64" 2>/dev/null || true
fi

# Minimal /dev
mkdir -p "$NEWROOT/dev"
for dev in null zero urandom random; do
  touch "$NEWROOT/dev/$dev"
  mount --bind "/dev/$dev" "$NEWROOT/dev/$dev"
done
# /dev/fd, /dev/stdin, /dev/stdout, /dev/stderr
ln -sf /proc/self/fd "$NEWROOT/dev/fd" 2>/dev/null || true

# /proc (fresh mount for PID namespace)
mkdir -p "$NEWROOT/proc"

# /tmp (private tmpfs)
mkdir -p "$NEWROOT/tmp"
mount -t tmpfs tmpfs "$NEWROOT/tmp"
chmod 1777 "$NEWROOT/tmp"

# Minimal /etc (read-only essentials)
mkdir -p "$NEWROOT/etc"
for f in passwd group nsswitch.conf hosts; do
  [ -f "/etc/$f" ] && cp "/etc/$f" "$NEWROOT/etc/$f"
done
# DNS only if network is allowed
%s

# App's own data directory (read-write)
APP_DATA=%s
mkdir -p "$APP_DATA" "$NEWROOT$APP_DATA"
mount --bind "$APP_DATA" "$NEWROOT$APP_DATA"

# Tools directory (read-only) — apps call binaries from /home/alf/data/tools/
if [ -d /home/alf/data/tools ]; then
  mkdir -p "$NEWROOT/home/alf/data/tools"
  mount --rbind /home/alf/data/tools "$NEWROOT/home/alf/data/tools"
  mount -o remount,ro,bind "$NEWROOT/home/alf/data/tools" || { echo "FATAL: cannot make tools read-only"; exit 1; }
fi

# HOME skeleton
mkdir -p "$NEWROOT/home/alf"

# --- Phase 3: chroot into new root ---
# pivot_root fails on Docker overlay fs. chroot provides the same
# allowlist isolation. The old root becomes unreachable after chroot
# because the process loses CAP_SYS_ADMIN when we drop to uid 1000
# (the classic chroot escape requires caps the process won't have).
mount -t proc proc "$NEWROOT/proc" 2>/dev/null || true

# --- Phase 4: Write the user command into the sandbox ---
# The command is passed via __SANDBOX_CMD env var (set by Go).
# This avoids all shell quoting issues — the variable is never
# interpreted by the setup script's shell.
cat > "$NEWROOT/tmp/run.sh" << 'SANDBOX_SCRIPT'
#!/bin/bash
ulimit -v 131072 2>/dev/null || true
ulimit -u 256 2>/dev/null || true
ulimit -f 102400 2>/dev/null || true
ulimit -t 60 2>/dev/null || true
exec /bin/bash -c "$__SANDBOX_CMD"
SANDBOX_SCRIPT
chmod 755 "$NEWROOT/tmp/run.sh"

# --- Phase 5: chroot + drop to uid 1000 and execute ---
# chroot changes root, setpriv drops all caps permanently.
# Without CAP_SYS_ADMIN the process cannot escape the chroot.
exec /usr/sbin/chroot "$NEWROOT" \
  setpriv --reuid=1000 --regid=1000 --init-groups --inh-caps=-all \
  /bin/bash /tmp/run.sh
`,
		dnsSnippet(cfg.Network),
		shellQuote(cfg.AppDataDir),
	)

	cmd.Path = "/bin/bash"
	cmd.Args = []string{"bash", "-c", setup}

	// Fork as root (uid 0) to perform mounts. The daemon runs as uid 1001
	// (alfd) with CAP_SETUID — Credential{Uid:0} switches the child to root.
	// mount(2) requires euid==0 in the mount namespace even with CAP_SYS_ADMIN.
	// The script drops to uid 1000 via setpriv after chroot completes.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: flags,
		Credential: &syscall.Credential{Uid: 0, Gid: 0},
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

