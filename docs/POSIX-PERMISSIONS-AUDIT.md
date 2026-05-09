# POSIX file-permission audit (#407)

This audit categorises every `os.Chmod`, `os.MkdirAll(_, mode)`,
and `os.WriteFile(_, _, mode)` call in the alf codebase against
the layered architecture defined in
[`docs/ARCHITECTURE-SECURITY.md`](ARCHITECTURE-SECURITY.md). It
is the deliverable for ticket #407 — a sibling of #86 (Layer 1
outer ring: AppArmor + seccomp + CAP_SYS_ADMIN drop).

## How POSIX perms fit the 3-layer model

POSIX file permissions live at the **kernel ring** of Layer 1
(see §2.1). They are defense-in-depth below the AppArmor profile
(#86) — both express "what the daemon process can touch" but at
different levels:

- **AppArmor** restricts paths at the LSM hook level, **before**
  the syscall reaches the filesystem. Defines what the daemon
  process is *capable of* touching.
- **POSIX perms** restrict access at the inode level. Defines
  what *any process running as a given uid/gid* is *allowed to*
  touch.

The two channels are independent. AppArmor mis-config does not
expose files that POSIX guards already deny; conversely, a leaked
fd or a setuid escape into a different uid still hits POSIX
checks. Belt-and-braces.

## Process identities

The container's userspace topology (set by the Dockerfile +
entrypoint, gated by #86's CAP_SYS_ADMIN drop work):

| User | UID | Role |
|---|---|---|
| `alfd` | (root in container today; #86 will drop) | Daemon process. Owns `<dataDir>/` and most artefacts. |
| `alf` | 1000 | LLM subprocess (Claude / Codex CLI runs here). The LLM reads/writes scoped portions of `<dataDir>/agents/`, `<dataDir>/apps/<slug>/data/`, `<dataDir>/skills.d/`. |

Both uids are members of the `alf` group (gid 1000) so artefacts
written by one are readable by the other when group-readable.

`syscall.Umask(0o002)` runs at daemon boot
(`cmd/alf-daemon/main.go:104`). umask 0o002 clears the
world-write bit on every newly-created inode but does **not**
restrict reads — a `MkdirAll(_, 0o755)` stays `0o755`. The umask
narrows intent (no accidental world-write); does not widen.

## Trust domains and their POSIX boundaries

The chmod sites group naturally into three trust domains aligned
with the architecture:

### Domain 1 — Admin trust (§6, §7.5)

Material that the **agent / LLM is forbidden to touch**. Tightest
permissions, owner-only access, `0o700` dirs and `0o600` files.

| Path | Mode | Site | Justification |
|---|---|---|---|
| `<dataDir>/keys/` | dir 0o700 | `wasm/daemonkey.go:92`, `admin/userkey/userkey.go:402` | Tier-2 daemon key + Tier-3 user-endorsed key. Owner-only. |
| `<dataDir>/keys/daemon.json` | file 0o600 | `wasm/daemonkey.go:109` | Private signing key. Strictest. |
| `<dataDir>/keys/user-endorsed.json` | file 0o600 | `admin/userkey/userkey.go:419` | Passphrase-encrypted, but the passphrase isn't part of POSIX — defense in depth keeps the ciphertext owner-only. |
| `<dataDir>/admin/pending/` | dir 0o700 | `admin/pending/dir.go:89` | Ratification queue items. Construction refuses 0o077 perms (DirStore guard). |
| `<dataDir>/admin/pending/<id>.json` | file 0o600 | `admin/pending/dir.go:230` | Per-Item JSON; atomic tmp+rename. |

Verdict: **strict, no tightening needed.** The admin boundary
archtest (`TestAdminPackageBoundary`) prevents capability/runtime
code from importing `internal/admin/*`, so even a buggy
capability cannot ask for these handles.

### Domain 2 — Process-isolation (§4.4, §7.5)

Sockets and per-process artefacts that the daemon and LLM
subprocess BOTH need access to via the `alf` group, but no other
process should reach.

| Path | Mode | Site | Justification |
|---|---|---|---|
| `<dataDir>/signal*.sock` | 0o660 | `platform/signal/server.go:60` | rw for daemon + alf group; world has no access. |
| `<dataDir>/tools_proxy.sock` | 0o660 | `controlcenter/tools_proxy.go:162,187` | Same. |
| `<dataDir>/memory.sock` | 0o660 | `memory/socketsrv/server.go:280` | Same. |
| `<dataDir>/sandbox/secrets/*.sock` | 0o660 | `sandbox/secrets/proxy.go:168` | Vault socket. |
| `<dataDir>/scheduler.sock` | 0o660 | `scheduler/server.go:71` | Same. |
| `<dataDir>/agents/<task>/` | dir 0o775 | `runtime/agents/orchestrator.go:290,891` | LLM subprocess writes here; group-write required. World cannot write (umask 002 on read still applies). |
| Quarantined tool | file 0o640 | `sandbox/integrity/guard.go:604` | rw for owner + group-read. **No execute** so the LLM cannot bypass quarantine via `bash` invocation. Also `chgrp alfd` runs alongside (lines 603-606). |

Verdict: **all 0o660 sockets are correctly scoped** — alf group
membership is the gate. **Quarantine 0o640 is the load-bearing
defense** against the LLM re-running a tampered tool;
double-checked by the gid swap to alfd. **0o775 agents dirs are
intentional** — the LLM subprocess writes there.

### Domain 3 — Public artefacts (CC + media)

Files served by the Control Center HTTP server or consumed by
sibling alf-group processes. World-readable is fine because the
CC's own auth gate (cookie + CSRF + magic link) controls actual
access; POSIX perms here are just "no surprises if someone
ls-es".

| Path | Mode | Site | Justification |
|---|---|---|---|
| `<dataDir>/media/*.png` etc | 0o644 | `platform/media/frames.go:77,86,233,288` | Media frames served via CC routes. |
| `<dataDir>/context/*` | 0o664 | `cmd/alf-daemon/bootstrap.go:428` | Group-readable so peer alf-group processes (claude CLI subprocess) can consume. |
| `<dataDir>/docs/*.md` | 0o444 | `cmd/alf-daemon/bootstrap.go:401` | **Read-only** so the LLM cannot modify the bundled docs it consumes. |
| `<dataDir>/llms.txt` | 0o444 | `cmd/alf-daemon/bootstrap.go:406` | Same. |
| `<dataDir>/apps/<slug>/bin/*` | 0o555 | `marketplace/manager.go:680,682` | Read+exec, no write. Marketplace lock — LLM can't tamper with installed apps. |
| `<dataDir>/apps/<slug>/*` | 0o444 | `marketplace/manager.go:684` | Read-only after install. |

Verdict: **acceptable.** The 0o444 read-only marketplace lock is
load-bearing: a tampered app fails the integrity guard's hash
check at next start. The 0o644/0o664 media/context files have no
secret content (everything served via CC is gated by the CC auth
boundary).

### Trust store edge case

`<dataDir>/trust/` is created with `MkdirAll(_, 0o755)` in
[`internal/capability/envelope/truststore.go:339,388`](../internal/capability/envelope/truststore.go).
Pubkey files are written 0o644 ([`atomicWrite` default](../internal/capability/envelope/truststore.go)).

Public keys are **public material by design** — operators import
them from third parties via `alf trust add`. World-read of the
pubkey bytes is harmless. The LIST of installed publishers
(directory listing) is operator metadata; an attacker who can
read the dir learns who you trust but not what they signed.

**Tightening candidate (deferred to #86)**: under the post-#86
uid/gid model where the LLM runs in a separate group, the trust
dir should drop to 0o750 (root group only) so the LLM cannot
enumerate operator-trusted publishers. Until #86 lands, the alf
group includes the LLM subprocess and 0o750 would be no
different from 0o755 in practice.

## Special cases

### Marketplace install lockdown

`marketplace/manager.go:680-700` walks the installed app dir and
chmods every file: 0o555 for executables, 0o444 for everything
else. This is the load-bearing defense against the LLM
hand-modifying an installed app's binary or web assets between
restarts. The integrity guard
([`sandbox/integrity/guard.go`](../internal/sandbox/integrity/guard.go))
re-hashes on next start and quarantines anything that drifted.

### Bootstrap docs writable-then-locked

`cmd/alf-daemon/bootstrap.go:400, 405` chmods to 0o644 just
before `WriteFile` overwrites with 0o444. The intermediate 0o644
window exists because the previous boot wrote 0o444 and Go's
`WriteFile` cannot overwrite a read-only file. Window is
sub-millisecond and the daemon is the only writer at boot. No
race.

### Upgrade binary

`cmd/alf/upgrade.go:137,396` chmods the downloaded `alf` binary
0o755 before exec'ing — needed for `os.Rename` to work over a
read-only target. This is the host-side `alf upgrade` path, not
the in-container daemon.

## Cross-checks

### vs umask 0o002

`syscall.Umask(0o002)` (`cmd/alf-daemon/main.go:104`) clears the
world-write bit on every newly-created inode. **Effect:**

| Requested | Effective |
|---|---|
| 0o777 | 0o775 |
| 0o755 | 0o755 |
| 0o700 | 0o700 |
| 0o644 | 0o644 |
| 0o660 | 0o660 |

The umask **narrows intent on the world-write bit only**. Read
bits are honoured as requested. No widening.

### vs AppArmor (#86 forward-look)

When #86 lands, the AppArmor profile will define:
- Daemon process: read+write under `<dataDir>/`, `<containerVol>/`,
  read-only under `/opt/alf/`, no access to `/etc/shadow` /
  `/proc/<other>/`.
- LLM subprocess: read+write under `<dataDir>/agents/<task>/`,
  read+exec under `/opt/alf/tools.d/`, no access to
  `<dataDir>/keys/`, `<dataDir>/admin/`, `<dataDir>/trust/`.

The POSIX perms documented above will become defense-in-depth
below those AppArmor rules. The 0o700 + 0o600 admin dirs already
match what the AppArmor profile will deny anyway.

### vs CAP_SYS_ADMIN drop (#86 forward-look)

CAP_SYS_ADMIN drop will require the daemon to run as a
non-privileged user (today it runs as root for sandbox setup).
Once that's done:
- All `os.Chown` calls in `runtime/agents/orchestrator.go` and
  `sandbox/integrity/guard.go` need updating — chown to a uid
  the process doesn't own will EPERM.
- The integrity-guard's gid swap to `alfd` (`lockdownTool`,
  line 605) becomes the load-bearing isolation between daemon and
  LLM. Currently augmented by uid 0 capability; under #86 the gid
  will be the only mechanism — so it must be set up correctly at
  container boot before the daemon drops privs.

## Findings — no immediate fixes required

The audit identified **zero violations** of the trust-domain
boundaries. Every `0o600` / `0o700` site is in the admin
domain, every `0o660` socket is in the process-isolation domain,
every `0o644`/`0o644` artefact is in the public-via-CC domain.

**Two follow-ups deferred to #86:**

1. **`<dataDir>/trust/` to 0o750** when the LLM gets its own
   gid (post-#86). Pure POSIX hardening; no functional change
   today because the alf group spans both daemon and LLM.
2. **chown call sites** need updating when the daemon drops to
   non-root. Track in #86 — not separately actionable here.

## Maintenance

When adding a new chmod-like call:

1. Identify the trust domain (Admin / Process-isolation / Public).
2. Pick the matching mode from the table above.
3. If a new mode is needed, add a row here AND a justification
   comment in the source.

A future archtest `TestPosixPermissionDomains` could pin this
mapping by static analysis. Deferred — the call sites are stable
enough that ad-hoc review per PR is sufficient for now.

## References

- [`docs/ARCHITECTURE-SECURITY.md`](ARCHITECTURE-SECURITY.md)
  §2.1 (Layer 1 outer ring), §6 (admin boundary), §7.5 (vault
  partitioning).
- #86 — AppArmor profile + seccomp + CAP_SYS_ADMIN drop
  (sibling).
- #406 — sandbox demolition (where this audit was deferred from).
