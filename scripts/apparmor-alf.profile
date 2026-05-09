# AppArmor profile for the alf container (#86, SEC-A01).
#
# Replaces `apparmor=unconfined` from the v0.7.x layout. Loaded via
#   security_opt: apparmor=alf
# in docker-compose.yml.tmpl.
#
# **Loading**. Operators install once via:
#
#     sudo apparmor_parser -r -W /opt/alf/scripts/apparmor-alf.profile
#
# Verify with `aa-status | grep alf`. The Docker container then
# refers to it by name (`apparmor=alf`).
#
# **Posture**. The pre-#406 sandbox needed `mount`, `pivot_root`,
# and CAP_SYS_ADMIN to set up bwrap jails. After #406 razed that
# layer, the daemon process performs no such operations — the
# WASM tier runs entirely in-process via wazero (Layer 1 inner
# ring) and the Go-kind tier is TCB. So this profile is a
# narrow-syscall posture: deny mount / pivot_root / mknod /
# kernel-module loads, allow ptrace only for self (Go runtime
# uses ptrace internally for some operations).
#
# **What this profile does NOT do**. It does not replace the
# wazero inner ring (those are different threat models). It does
# not contain a CAP_SYS_ADMIN escape — that's #86's CAP drop
# work, not the AA profile. It does not constrain network paths
# (Docker net + iptables-OWNER rules cover that).
#
# Tested against: alf-daemon Phase 1 (apt-get install during
# entrypoint), Phase 2.9 (nettrack-helper), Phase 3 (daemon main
# + LLM subprocess via setpriv).

#include <tunables/global>

profile alf flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/bash>
  #include <abstractions/consoles>
  #include <abstractions/nameservice>

  # Allow normal POSIX file access — the daemon reads/writes
  # under /home/alf/data, /opt/alf, /tmp, /run. Capabilities are
  # the gate for sensitive paths (keys, admin), POSIX perms +
  # AppArmor's path-based rules are the next layer.
  /home/alf/** rwlk,
  /opt/alf/** rwlk,
  /tmp/** rwlk,
  /run/** rwlk,
  /var/run/** rwlk,
  /var/log/** rwl,

  # Read-only system paths — the LLM subprocess (and helpers) need
  # these to function (libc resolution, /etc/passwd lookups, etc.).
  /etc/** r,
  /usr/** rmix,    # rmix = read + memory-map + inherit + execute
  /lib/** rmix,
  /lib64/** rmix,
  /bin/** rmix,
  /sbin/** rmix,

  # Proc / sys — read-only for self; deny writes that could
  # influence kernel state.
  /proc/** r,
  /proc/[0-9]*/fd/* rwk,
  /proc/[0-9]*/task/** rk,
  /sys/** r,
  deny /sys/kernel/** w,
  deny /sys/fs/cgroup/** w,
  deny /proc/sys/** w,
  deny /proc/sysrq-trigger w,

  # Network — allow IPv4/IPv6/Unix as the daemon needs HTTP
  # outgoing + Unix socket IPC. Block raw sockets (CAP_NET_RAW
  # is already dropped in cap_drop=ALL but explicit here).
  network inet,
  network inet6,
  network unix,
  deny network raw,
  deny network packet,
  deny network netlink raw,

  # Capabilities — the cap_add list in docker-compose is already
  # narrow (after #86 cap_add drop). Mirror the allowed set here
  # as a belt-and-braces.
  capability setuid,
  capability setgid,
  capability dac_override,
  capability fowner,
  capability chown,
  capability net_admin,    # nettrack-helper

  # Explicit denies that map to the deferred items in #406:
  deny capability sys_admin,
  deny capability sys_chroot,
  deny capability sys_module,
  deny capability sys_rawio,
  deny capability sys_ptrace,
  deny capability mac_admin,
  deny capability mac_override,

  # Filesystem mutation operations the daemon does not need.
  deny mount,
  deny umount,
  deny pivot_root,
  deny mknod,
  # Note: chroot deny would break legitimate apt-get during Phase 1
  # if it ever needed it — left allowed for now; revisit when
  # CAP_SYS_CHROOT is dropped from cap_add (sibling work).

  # Ptrace — Go runtime uses it internally for some operations
  # (rare, but does happen). Limit to self.
  ptrace (read,trace) peer=alf,
  deny ptrace peer=/usr/sbin/sshd,

  # Signals — daemon spawns LLM subprocess and may send SIGTERM.
  # Allow self-signals; deny signals to other-confined peers.
  signal (send,receive) peer=alf,
  signal (send,receive) peer=unconfined,

  # Deny load_module / unload_module / kexec_load / etc. via
  # capability; explicit here in case future kernels expose them
  # via paths.
  deny /usr/sbin/insmod x,
  deny /usr/sbin/rmmod x,
  deny /usr/sbin/modprobe x,
}
