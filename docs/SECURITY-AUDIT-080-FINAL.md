# Security audit — release/0.8.0 final-tag readiness

> Auditor: security-auditor sub-agent — 2026-05-09
> Branch: `release/0.8.0` @ `60b3aac`
> Scope: §1–§13 of `docs/ARCHITECTURE-SECURITY.md` versus shipped code under `cmd/alf-daemon/`, `internal/runtime/`, `internal/capability/`, `internal/marketplace/`, `internal/platform/`, `internal/sandbox/`, `internal/controlcenter/`, `internal/cli/`, `internal/admin/`, `cmd/nettrack-helper/`, `scripts/`.
> Methodology: read every doc reference, verify every claim against source, mentally walk attack vectors per §3 (3-layer model), §7 (trust + envelope), §8 (revocation), §10 (admin boundary), §12 (milestone gate). Specific commits checked: `fa73937` (strict-flip), `4a4c5a0` (SEC-407-001/002), `1da3521` (#384), `948a48a` (#403), `639c95d` / `7ec5022` / `7384785` / `ab96a9c` / `31524d5` (#392 stages), `5529eeb` (#396 D2/D8), `aee92e5` (#402), `40ef4dc` (#86), `fe18a73` (LLM cap drop).

---

## Resolution status (2026-05-13)

All 10 findings have been triaged. Status as of branch `release/0.8.0` HEAD:

| Finding | Severity | Status | Fix commit |
|---|---|---|---|
| SEC-080-001 | HIGH | resolved | `0a97910` — recheck trust store inside `trackLive` |
| SEC-080-002 | HIGH | resolved | `9617570` — orchestrator chmod/chown via `O_NOFOLLOW` fd |
| SEC-080-003 | HIGH | resolved | `3af0bab` — nettrack-helper sockets via `socknet.ListenUnix0660` |
| SEC-080-004 | MEDIUM | resolved | `33d9775` — updater verifier wiring is fail-closed |
| SEC-080-005 | MEDIUM | resolved | `472b71a` — kernel-prompt nonce fails loud on PRNG error |
| SEC-080-006 | MEDIUM | resolved | `28e41d4` — `EnforceTier2Ceiling` gates `kind` / `[[depends]]` / `[[raw_imports]]` |
| SEC-080-007 | MEDIUM | resolved | `cd97894` — `dev-deploy --enforce` activates Layer 1+2 (closes #417) |
| SEC-080-008 | LOW | resolved | `d1e76ad` — strict-flip residual cleanups |
| SEC-080-009 | LOW | accepted | `marketplace_pubkey.minisign` empty by design (no marketplace bundles in 0.8.0) |
| SEC-080-010 | LOW | resolved | `d1e76ad` — strict-flip residual cleanups |

**Verdict (original):** TAG BLOCKED. **Verdict (after triage):** all HIGH and MEDIUM findings either resolved or accepted with documented rationale; the original blockers no longer apply.

The "Findings" section below is preserved verbatim as the original auditor's report. The annotation above is the only addition.

---

## Verdict

**TAG BLOCKED.** Three real findings need resolution or explicit operator-facing acknowledgement before tagging:

- **SEC-080-001 (HIGH)** — TOCTOU between `envelope.Verify` and `trackLive` lets a freshly-instantiated bundle escape a concurrent revocation. New live Instance signed by a revoked key.
- **SEC-080-002 (HIGH)** — Residual TOCTOU on top of the SEC-407-001 fix in `internal/runtime/agents/orchestrator.go`: `Lstat` is advisory, not a fence, against the subsequent `os.Chmod` / `os.Chown`. The LLM owns the parent directory; race window is narrow but exploitable in principle.
- **SEC-080-003 (HIGH)** — `cmd/nettrack-helper/main.go` opens two Unix sockets via `net.Listen` then `os.Chmod(0o660)`, exactly the pattern SEC-407-002 fixed elsewhere. The control socket lets any uid in the container toggle the iptables kill switch during the open-mode window.

Plus four MEDIUM findings (cosign default fail-open + misleading log, kernel-prompt nonce fail-open on `crypto/rand` failure, `EnforceTier2Ceiling` does not gate on `[[depends]]` / `[[raw_imports]]` / `[provider.exports]`, AppArmor + seccomp profiles shipped but not activated by default in `docker-compose.yml.tmpl`).

Plus three LOW findings (`internal/sandbox/exec/exec.go` log lines still say "experimental" post strict-flip, `marketplace_pubkey.minisign` is empty in this checkout — verified intentional, deprecation helper warns for any non-empty `ALF_EXPERIMENTAL` value including `=0`).

The HIGH-severity findings are exploit-narrow but real architectural breaks: SEC-080-001 contradicts §8's revocation invariant; SEC-080-002 leaves residual TOCTOU in the post-#407-fix code; SEC-080-003 is a missed site of the same pattern that was already extracted into `internal/platform/socknet/socknet.go`.

---

## Findings

### SEC-080-001 (HIGH) — Revocation cascade TOCTOU between Verify and trackLive

**Severity:** HIGH (block tag — invariant break, narrow but real exploitability)
**CWE:** CWE-367 (TOCTOU race condition)
**Invariant violated:** §8 "Cascade — revoking a fingerprint invalidates every bundle it signed, past and future"; §7.7 "the cascade machinery is now ready"; §9 hard rule 7 "One forge — every load path converges on `Runtime.Instantiate`"

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/instantiator_verified.go:90-196` — `InstantiateVerified`:
  - L91 `vm, err := envelope.Verify(in)` — reads trust store under `RLock`
  - L185 `inst, err := handle.ForgeInstance(...)` — wazero compile + bind happens before this
  - L196 `i.trackLive(inst, vm.SignerID, vm.SignedAt, dependsOnKeys(vm.Manifest))` — instance only enters the live registry HERE
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/cascade.go:104-125` — `Refresh()`:
  - L108 `current := c.snapshotter()` — reads trust store under `RLock`
  - L116 `c.inst.RevokeByKey(k)` — closes Instances currently in `i.live`. An instance that has not yet reached `trackLive` is invisible.
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/revocation_cascade.go:57-73` — SIGHUP handler: calls `wasmRt.TrustStore.Load()` then `cascader.Refresh()`. Concurrent goroutines that started `InstantiateVerified` BEFORE the SIGHUP can have already-validated a now-revoked key.

**Concrete sequence:**

1. Goroutine A (loading bundle B, signed by key K): enters `InstantiateVerified` → `envelope.Verify(in)` returns success because K is currently trusted and not revoked. A is now between line 91 and line 196.
2. Operator runs `alf trust revoke <K>`. `<K>.revoked` sidecar lands on disk.
3. SIGHUP fires. Signal handler calls `wasmRt.TrustStore.Load()` (re-reads sidecars, K is now revoked-after-`now`), then `cascader.Refresh()`.
4. Refresh() snapshots → diff against `c.last` finds K newly revoked → `Instantiator.RevokeByKey(K)` runs. `i.live` contains no entry for K (A has not yet reached line 196). RevokeByKey returns an empty slice.
5. Goroutine A reaches line 196: `trackLive(inst, K, vm.SignedAt, ...)` registers an Instance signed by a revoked key. **Bundle B is now live with authority granted from a revoked key. No further revocation discovery channel will fire for K (the transition has already been processed).**

**Window size:** Verify → ForgeInstance is ~10–500 ms for a typical WASM bundle (TOML parse + canonicalisation + Ed25519-ph + BLAKE2b-512 + wazero compile + linking + `_initialize` reactor invocation). For `marketplace-app` consumers via `internal/marketplace/bundle.go:169`, the window is bounded by the `verifyBundle → return` round-trip. Narrow, but a live SIGHUP during boot or during a marketplace install hits exactly the configuration where an operator is racing to revoke.

**Recommended remediation:**

Two patterns, either is sufficient:

1. **Re-check inside `trackLive`.** After acquiring `i.liveMu`, look up the instance's `signerID` against the trust store's current `RevokedAfter()`. If revoked at-or-before the bundle's `SignedAt`, immediately `inst.Close()` and do not append to `i.live`. Return a typed error so the caller fails closed.
2. **Single linearised lock.** Have `RevokeByKey` and `trackLive` share a serialised mutex with `Verify`'s read of the trust store, so an in-flight Verify either finishes before revocation snapshots or visibly fails afterwards. Heavier, but easier to argue about.

Pattern 1 is cheaper; combine with a unit test that races `RevokeByKey(K)` against `InstantiateVerified` for a bundle signed by K and asserts the resulting Instance is closed before `trackLive` returns.

**Compliance impact:** OWASP A07 (Identification and Authentication Failures); SOC 2 CC6.1 (Logical Access Controls); breaks §8 "Cascade" promise.

---

### SEC-080-002 (HIGH) — Residual TOCTOU on top of SEC-407-001 fix

**Severity:** HIGH (block tag — known-pattern hole, same domain crossing as the original CRITICAL)
**CWE:** CWE-367 (TOCTOU); CWE-59 (Symbolic link following)
**Invariant violated:** §2.1 Layer 1 outer ring — "POSIX file-mode hygiene — every artefact under `<dataDir>/` carries a mode aligned with its trust domain"; §6 admin boundary "never LLM-reachable"

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/agents/orchestrator.go:288-317`:
  - L288 `agentsParent := filepath.Join(o.dataDir, "agents")` — owned by `alf:alf` (uid 1000, the LLM) per `scripts/entrypoint.sh:123`.
  - L297 `if info, err := os.Lstat(agentsParent); err == nil && info.Mode()&os.ModeSymlink != 0` — refuses if symlink **at the moment of Lstat**.
  - L301 `os.MkdirAll(taskDir, 0o775)` — between Lstat and this call, the LLM can `rename agents agents.bak; ln -s /home/alf/data/keys agents`.
  - L310 `os.Chmod(agentsParent, 0o775)` — follows symlinks. If the LLM won the race in the previous bullet, this chmods `/home/alf/data/keys` (the admin domain).
  - L311 `os.Chown(agentsParent, 1000, 1000)` — same. Now `keys/` is owned by the LLM.
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/agents/orchestrator.go:902-922` — same pattern at `agentDir` (per-agent subdir). Lstat at L912 followed by Chmod / Chown at L920–L921 with no NOFOLLOW.

The fix in commit `4a4c5a0` correctly closed the trivial "swap before any check" version of SEC-407-001, but `os.Lstat` is read-once advisory and `os.Chmod` will dereference a symlink installed in the intervening microseconds. The LLM owns `agents/` writeably (g+ws on the parent per entrypoint), so it can rename + symlink in a tight loop.

**Recommended remediation:**

Replace the Lstat → Chmod pattern with one of:

1. **`fchmodat(dir_fd, ".", mode, AT_SYMLINK_NOFOLLOW)`** — POSIX fence at the moment of mode change. Available via `golang.org/x/sys/unix.Fchmodat` with the right flag.
2. **Open + `f.Chmod`.** `os.OpenFile(agentsParent, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)` then `f.Chmod(0o775)`. The `os.O_NOFOLLOW` flag refuses to open if the path component is a symlink at open time, and `f.Chmod` operates on the FD (no path resolution).
3. **`open(.., O_PATH | O_NOFOLLOW)` + `fchmodat(fd, "", mode, AT_EMPTY_PATH)`.** Linux-specific, strongest.

Approach 2 is portable and Go-stdlib-only.

The same pattern fix applies to all three sites (lines 297, 304, 912). Add a test that creates a directory, replaces it with a symlink to `/tmp/x` between Lstat and Chmod via a separate goroutine, and asserts the daemon refuses to chmod / chown.

**Compliance impact:** Same as SEC-407-001 — admin-domain escalation by the LLM. OWASP A01 (Broken Access Control).

**Defense-in-depth:** Activating the AppArmor profile (currently shipped at `scripts/apparmor-alf.profile` but unused per docker-compose template) would block the chmod into `<dataDir>/keys/` regardless of the TOCTOU.

---

### SEC-080-003 (HIGH) — nettrack-helper Unix sockets retain pre-#407 TOCTOU pattern

**Severity:** HIGH (block tag — known-pattern hole missed during SEC-407-002 cleanup)
**CWE:** CWE-367 (TOCTOU); CWE-732 (Incorrect Permission Assignment)
**Invariant violated:** §2.1 Layer 1 outer ring — POSIX file-mode hygiene + §10 admin boundary "never LLM-reachable" (the kill-switch socket is an admin-domain control surface)

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/cmd/nettrack-helper/main.go:83-92` — `runControlSocket`:
  ```go
  os.Remove(ctrlSockPath)
  ln, err := net.Listen("unix", ctrlSockPath)        // L85: opens at umask-default mode
  ...
  os.Chown(ctrlSockPath, 0, 1001)                    // L91
  os.Chmod(ctrlSockPath, 0o660)                      // L92: too late
  ```
- `/Users/alessandrolamparelli/Dev/alf/cmd/nettrack-helper/main.go:127-137` — `main`:
  ```go
  os.Remove(sockPath)
  ln, err := net.Listen("unix", sockPath)            // L129
  ...
  os.Chown(sockPath, 0, 1001)                        // L136
  os.Chmod(sockPath, 0o660)                          // L137
  ```

Both sites use the same pre-fix pattern that SEC-407-002 extracted into `internal/platform/socknet/socknet.go::ListenUnix0660`. The `internal/platform/socknet` package was created precisely to share this fix; nettrack-helper was not migrated.

**Why it matters:** the **control socket** at `/run/alf-nettrack-ctrl.sock` accepts JSON commands of shape `{"kill_switch": bool}` and toggles iptables. The intended caller is the daemon (uid 1001). During the ~µs window between `net.Listen` and `os.Chmod`, the socket is reachable from any process in the container that can `connect(2)` — including the LLM subprocess (uid 1000). With umask 0o022 (kernel default for /run), the open-mode is 0o755; with the daemon's umask 0o002 (set in main.go:101 of cmd/alf-daemon for daemon-context creates, not nettrack-helper which is a separate binary), the open-mode is 0o775 — both world-connectable.

The data socket at `/run/alf-nettrack.sock` is read-only output to the daemon, so the window only leaks conntrack metadata that the LLM might learn faster than via netlink. Lower impact, same pattern.

**Concrete attack:** the LLM, racing in a tight loop, connects to `/run/alf-nettrack-ctrl.sock` during the open-mode window and sends `{"kill_switch": true}`. iptables drops every non-loopback outbound connection — the daemon loses its outbound HTTP, marketplace updates, and CRL refresher. Or `{"kill_switch": false}` to ensure the kill switch is never engaged when the operator wants it. Either way the LLM influences a network-policy control surface it has no legitimate handle to.

**Recommended remediation:**

Migrate nettrack-helper to `internal/platform/socknet`:

```go
import "github.com/alamparelli/alf/internal/platform/socknet"

ln, err := socknet.ListenUnix0660(ctrlSockPath, 1001)
```

Note: `socknet.ListenUnix0660` is currently parameterised on a `gid` argument that defaults to 1000 in every existing call site. nettrack-helper needs gid 1001 (alfd) for the control socket. Either pass the argument or extend the helper to accept a chown target separately. The umask wrap inside socknet (currently 0o117 → 0o660 mode) is independent of the chown target, so the fix is mechanical.

Add a test that races a `connect(2)` from a goroutine started before `ListenUnix0660` returns and asserts ECONNREFUSED.

**Compliance impact:** OWASP A01 (Broken Access Control); the kill switch is a network-control surface; the LLM should never reach it.

---

### SEC-080-004 (MEDIUM) — Updater fail-open default + misleading operator log

**Severity:** MEDIUM (track post-tag — design issue with operator-facing implication)
**CWE:** CWE-636 (Not failing securely); CWE-209 (Information Exposure through Misleading Comments)
**Invariant violated:** §7.1 "Algorithm pinning — verification dispatches on the declared algorithm; there is no silent default, no algorithm negotiation, no `algorithm: none`" extends in spirit to release-distribution; §13 "Defense-in-depth marketing" — each layer must name a specific attack class it defeats.

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/platform/updater/checker.go:183-197`:
  ```go
  c.mu.Lock()
  verifier := c.verifier
  c.mu.Unlock()
  if verifier != nil {
      ...verify and abort on failure...
  } else {
      log.Printf("update-check: no cosign verifier wired — proceeding without signature check (set ALF_DISABLE_COSIGN_VERIFY=1 to silence)")
  }
  ```
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/main.go:1325-1341` — production wiring sets the verifier unless `ALF_DISABLE_COSIGN_VERIFY=1`. Per the comment "the daemon's production wiring always sets one", the `else` branch should be unreachable in production. But:

**Two issues:**

1. **The log message is backwards.** It says "set `ALF_DISABLE_COSIGN_VERIFY=1` to silence". `ALF_DISABLE_COSIGN_VERIFY=1` is the variable that **disables verification entirely** (per main.go:1325) — it does not silence the warning, it justifies its absence. An operator following this advice would *increase* their attack surface, not silence the warning. The text should be something like "the daemon was constructed without a verifier — this is a programmer wiring bug; expected production code path always sets one".

2. **Fail-open posture.** When the verifier is nil for any reason (wiring bug, future refactor that moves construction, dev paths bypassing main.go), the updater notifies on every newer tag without signature validation. A mistaken refactor that drops the wiring silently regresses #403. Better posture: refuse to notify when no verifier is wired, and require an explicit `ALF_DISABLE_COSIGN_VERIFY=1` to opt into the no-verify path. This makes the security property "verification or no notification at all", with the operator explicitly opting out.

**Recommended remediation:**

```go
if verifier == nil {
    log.Printf("update-check: no cosign verifier wired — refusing to notify (this should not happen in production; check daemon wiring)")
    return
}
ctx, cancel := context.WithTimeout(...)
defer cancel()
repo := c.registry + "/" + c.repo
if err := verifier.Verify(ctx, repo, digest); err != nil {
    log.Printf("update-check: cosign verify %s@%s failed: %v — refusing to notify", repo, digest, err)
    return
}
log.Printf("update-check: cosign verify %s@%s ok", repo, digest)
```

Move the explicit "ok proceed without verify" logic into main.go:1325 — it stays as a no-op `SetCosignVerifier` only when `ALF_DISABLE_COSIGN_VERIFY=1`, in which case the code path is via a dummy verifier that returns nil.

**Compliance impact:** OWASP A02 (Cryptographic Failures — improper signature validation); SOC 2 CC6.6 (logical and physical access controls).

---

### SEC-080-005 (MEDIUM) — Kernel-prompt nonce fail-open on `crypto/rand` failure

**Severity:** MEDIUM (track post-tag — narrow exploit, but design is wrong)
**CWE:** CWE-330 (Use of Insufficiently Random Values); CWE-636 (Not failing securely)
**Invariant violated:** §3.2 "Stage 1 explicitly defers" — but the deferred properties were "memory tools surface", not "fail-secure on rand failure". §13 "prompt injection makes the LLM an authority vector → admin boundary + kernel prompt + non-authoritative markers on capability-provided content" — the kernel prompt's structural integrity rests on the per-Invoke nonce being unguessable.

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/llm/kernel_prompt.go:52-63`:
  ```go
  func NewNonce() string {
      var b [8]byte
      if _, err := rand.Read(b[:]); err != nil {
          // crypto/rand failure is catastrophic; fall back to a constant
          // rather than crash, so the daemon does not lose its LLM
          // pipeline. The constant still rotates between deployments
          // (different binary builds) — but a well-funded attacker
          // who reaches this branch already has bigger problems.
          return "0000000000000000"
      }
      return hex.EncodeToString(b[:])
  }
  ```
- `/Users/alessandrolamparelli/Dev/alf/internal/ai/provider/kernel_inject.go:22-28` — duplicate definition with the same fallback to `"0000000000000000"`:
  ```go
  func newMarkerNonce() string {
      var b [8]byte
      if _, err := rand.Read(b[:]); err != nil {
          return "0000000000000000"
      }
      return hex.EncodeToString(b[:])
  }
  ```

The fallback nonce is **a literal constant** known to anyone reading the code (open source). The comment claims "the constant still rotates between deployments (different binary builds)" — false: the literal is `0000000000000000` in the source, and rebuilding the binary doesn't change it.

**Concrete exploit:** if `crypto/rand.Read` fails for any reason (entropy-pool exhaustion, `/dev/urandom` not mounted, kernel `getrandom(2)` returns -EINTR repeatedly), `NewNonce()` silently returns `"0000000000000000"`. A WASM tool whose `WrapToolOutput` content includes the literal string `</tool_output_0000000000000000>` followed by `<system_pseudo>...injected instructions...</system_pseudo>` can break out of the wrap marker. The kernel prompt instructs the LLM to treat content inside `<tool_output_NONCE>...</tool_output_NONCE>` as non-authoritative; if the closing tag the LLM sees is the literal known constant, the LLM treats subsequent text as if it were outside the marker.

`crypto/rand` failures are not exotic in containers: an `ALF_DATA_DIR` on a tmpfs with no entropy device, a misconfigured `/dev/urandom` in a stripped-down image, or a kernel under load can produce short reads that surface as errors here.

The failure is **silent** — neither call site emits a log line. An operator has no way to detect the regression except by capturing prompts and noticing a hardcoded nonce in the markup.

**Recommended remediation:**

Fail loudly. Either:

1. **Refuse the Invoke.** Have `NewNonce()` (and `newMarkerNonce`) return `(string, error)` and propagate the error up to `KernelPromptInjector.Invoke`, which fails the LLM call.
2. **Log and use a hash of process state.** Even an emergency fallback should be unguessable per-process. Hash `os.Getpid() + time.Now().UnixNano() + binary build ID` if `crypto/rand` fails — and log loudly that the fallback fired.

Option 1 is correct under §3.2's threat model. The LLM pipeline being unavailable for a few seconds (until the next request retries) is preferable to a kernel prompt with a known break-out token.

Also: the duplicate definition between `internal/runtime/llm/kernel_prompt.go` and `internal/ai/provider/kernel_inject.go` is an architectural smell — the comment in `kernel_inject.go:10-15` notes the foundation-dependency rule forbids importing across that boundary, then duplicates the constants and functions instead. A shared package under `internal/platform/` would be cleaner; or invert the dependency so `kernel_prompt.go` lives where both can see it.

**Compliance impact:** OWASP A02 (Cryptographic Failures — insecure randomness); HIPAA §164.308(a)(5)(ii)(D) (Password Management — analogous principle for cryptographic material); the §3.2 agent-mediated boundary is the only thing standing between a malicious WASM tool and the user's memory under prompt injection.

---

### SEC-080-006 (MEDIUM) — `EnforceTier2Ceiling` does not gate `[[depends]]` / `[[raw_imports]]` / `[provider.exports]`

**Severity:** MEDIUM (track post-tag — exploit not reachable today, but architectural drift opens it later)
**CWE:** CWE-732 (Incorrect Permission Assignment)
**Invariant violated:** §7.3 Tier 2 — "Loading a tier-2-signed bundle that declares anything beyond the ceiling fails verification — the ceiling is re-checked at load time, not only at sign time"

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/ceiling.go:46-55` — `EnforceTier2Ceiling`:
  ```go
  func EnforceTier2Ceiling(m *Manifest) error {
      if m == nil { ... }
      if len(m.Events.Subscribes) > 0 {
          return fmt.Errorf("%w: events.subscribes ...", ErrCeilingExceeded, ...)
      }
      return nil
  }
  ```

The function checks only `events.subscribes`. The comment at lines 21-25 claims `http: none, exec: none, secrets: none, fs: own-dir, memory: agent-mediated` are "deferred block — already rejected" — true today, because `envelope.Validate` rejects those blocks via `ErrBlockDeferred`. But:

- `[[depends]]` is not deferred. A Tier-2-signed bundle CAN declare `[[depends]]` on `alf:fs`, `alf:http`, etc. The runtime resolves these via `HandleRegistry.Lookup` (instantiator_verified.go:278) and rejects unregistered handles. Today this means: a Tier-2 bundle CAN list `[[depends]] handle = "alf:fs"` and pass.

  In the current 0.8.0, `[[depends]]` validates registry presence but does NOT yet grant authority (forge wiring is Stage 5+). So today the bypass is benign. **However**, when Stage 5+ extends `forgeGrants` to consume `[[depends]]` entries, Tier-2 bundles will silently gain authority via the dependency chain unless `EnforceTier2Ceiling` is updated.

- `[[raw_imports]]` is not deferred. The schema has a default-deny classifier (`ErrRawImportForbidden` for `wasi:filesystem/`, etc.) but `wasi:clocks/`, `wasi:cli/{environment,exit,...}` are allowed. A Tier-2 bundle can declare these.

  Today the runtime side is not wired (WASI Preview 1 still in use; Preview 2 syntax doesn't map). The metadata travels signed but unread. **However**, when the runtime catches up, every Tier-2 bundle gets ambient WASI access without operator review.

- `[provider.exports]` is not deferred for `kind = capability-provider`. A Tier-2-signed `capability-provider` bundle would register handle kinds in the runtime registry under the daemon key's fingerprint short. The daemon key is auto-generated; the operator never reviewed any of those exports.

  Today the daemon-key bootstrap explicitly avoids signing `kind = capability-provider` bundles via the `kind` discriminator in instantiator_verified.go:209-215 (registers exports only when `Kind == KindCapabilityProvider`). But `EnforceTier2Ceiling` does not refuse `Kind = capability-provider`. Per §7.3 Tier 3, providers should require user-endorsed signing because they widen the trust surface; today an LLM that drops a `capability-provider` manifest into `<skillsDir>/wasm/<id>/` would have it auto-signed by the daemon key at next boot.

**Concrete attack today:**

1. The LLM drops a manifest with `kind = "capability-provider"` and a single `[[provider.exports]] id = "evil"` into `<skillsDir>/wasm/<id>/`.
2. Next boot: loader auto-signs (no Tier-2 ceiling check refuses it), forges, registers `<daemon-fp>:evil` in the registry.
3. Subsequent capability bundles (also LLM-built, also Tier-2-signed) declare `[[depends]] handle = "<daemon-fp>:evil"` and pass `resolveDepends`.

Under the current 0.8.0 code, this fails to grant authority because `forgeGrants` does not yet consume `[[depends]]` for non-alf namespaces. So the attack is **benign today** but is a latent escalation: the moment provider authority forging lands, the gate at the daemon-key boundary is missing.

**Recommended remediation:**

Extend `EnforceTier2Ceiling` to refuse:

1. **`Kind == KindCapabilityProvider`.** The daemon key cannot pre-approve trust-surface widening; Tier-3 (user-endorsed) is the right signer per §7.3.
2. **`len(m.Depends) > 0` where any entry's namespace is not `"alf"`.** Cross-publisher dependencies are a trust-widening surface that the daemon key should not pre-approve unilaterally.
3. **`len(m.RawImports) > 0`.** Even allowlisted raw imports should require explicit operator review — the Tier-2 bootstrap is "LLM authors a tool with the ambient defaults"; raw WASI imports are not ambient defaults.

Add tests covering each rejection. Update §7.3 Tier 2 ceiling table in ARCHITECTURE-SECURITY.md to enumerate these explicitly.

**Compliance impact:** OWASP A05 (Security Misconfiguration); SOC 2 CC6.1; PCI DSS 7.1 (least privilege).

---

### SEC-080-007 (MEDIUM) — AppArmor + seccomp profiles shipped but not activated

**Severity:** MEDIUM (track post-tag — operator-facing default; documented behaviour per §12 milestone row, but Layer 1 outer-ring claim is weakened)
**CWE:** CWE-1188 (Insecure Default Initialization of Resource)
**Invariant violated:** §2.1 outer ring — "AppArmor profile (#86) restricting syscall surface beyond Docker defaults" + "seccomp filter (#86) for the final syscall allowlist". The shipped template does not load either.

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/cli/templates/docker-compose.yml.tmpl:109-126`:
  ```yaml
  security_opt:
    # apparmor=unconfined is the legacy posture from the v0.7.x sandbox
    # ... Kept as `unconfined` until the profile has been soak-tested
    - apparmor=unconfined
    # Custom seccomp profile (#86 SEC-A11): activate via
    #   - seccomp=/opt/alf/scripts/seccomp-alf.json
    # See scripts/seccomp-alf.json. Same deferral rationale as
    # AppArmor: shipped, not yet activated by default.
  ```
- `/Users/alessandrolamparelli/Dev/alf/scripts/apparmor-alf.profile` — shipped, narrow-syscall posture, denies mount/pivot_root/SYS_ADMIN/SYS_MODULE/SYS_RAWIO.
- `/Users/alessandrolamparelli/Dev/alf/scripts/seccomp-alf.json` — shipped, Docker-default-derived allowlist.

The §2.1 outer ring claim that AppArmor + seccomp are part of the Layer 1 wall is **structurally true (profiles exist)** but **operationally false (operators get unconfined default)**. The template comment acknowledges this and points at the activation path. The §12 milestone row also acknowledges "Not flipped by default: docker-compose still ships `apparmor=unconfined`".

**Why I list this as MEDIUM rather than LOW:** the architecture document's Layer 1 promises hinge on these profiles. SEC-080-002 above can be defense-in-depth-blocked by the AppArmor profile (the chmod into `keys/` would be denied). With the profile inactive by default, the second wall isn't there.

This is an **operator-facing default**, not a code bug. But the doc and the deployment posture are out of sync.

**Recommended remediation:**

Two paths, choose one:

1. **Soak-test the profile against a full daemon boot.** This is the original plan per the comment. Once green, flip the template to `apparmor=alf` and add the `seccomp=/opt/alf/scripts/seccomp-alf.json` security-opt by default. Operators with custom profiles can override via `docker-compose.override.yml`. Update §12 milestone row to reflect the flip.
2. **Document the gap and ship as-is.** Add a prominent §2.1 footnote: "AppArmor + seccomp profiles ship with the daemon but are NOT activated by default in v0.8.0; operators must opt in via the steps in `scripts/apparmor-alf.profile`. Layer 1 outer ring is reduced to Docker `cap_drop` until activation. Tracked for default-on in v0.9.0."

Path 1 is the invariant-preserving one. Path 2 is honest about the actual security posture if path 1 isn't ready.

**Compliance impact:** OWASP A05 (Security Misconfiguration); SOC 2 CC6.6 — defense-in-depth not at documented strength.

---

### SEC-080-008 (LOW) — `internal/sandbox/exec/exec.go` log lines retain "experimental" wording post strict-flip

**Severity:** LOW (note — operator confusion, no security impact)

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/sandbox/exec/exec.go:40` (`SandboxedCmd`):
  ```go
  log.Printf("[sandbox] experimental: namespace isolation razed — uid drop only (ticket #406)")
  ```
- `/Users/alessandrolamparelli/Dev/alf/internal/sandbox/exec/exec.go:74` (`SandboxServerCmd`):
  ```go
  log.Printf("[sandbox] experimental: server isolation razed — uid drop only (ticket #406)")
  ```

The strict-flip retired the `ALF_EXPERIMENTAL` gate. Operators reading these log lines could believe the daemon is still in "experimental mode" — but it is the production mode, and the wording is leftover from the dev-window vocabulary.

**Recommended remediation:**

Change `[sandbox] experimental:` to `[sandbox] note:` or remove the lines entirely (uid-drop is now the normal posture; logging it on every spawn is noisy). The function comment at lines 11-22 already documents this clearly; the log is redundant.

**Compliance impact:** none (operator UX only).

---

### SEC-080-009 (LOW) — `marketplace_pubkey.minisign` is empty in this checkout — verified intentional

**Severity:** LOW (note — verified working as designed, documented for traceability)

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/marketplace_pubkey.minisign` — 0 bytes.
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/marketplace_key.go:39-49` — `MarketplacePublicKey()` returns `ErrNoMarketplaceKey` on empty file.
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/main.go:995-1000`:
  ```go
  if pub, err := envelope.MarketplacePublicKey(); err == nil {
      wasmRt.TrustStore.Add(pub)
      log.Printf("[marketplace] trust anchor: alf-marketplace key %s added to trust store", pub.ID.Hex())
  } else {
      log.Printf("[marketplace] no marketplace pubkey embedded — installs require operator-imported third-party keys (alf trust add)")
  }
  ```

The user's question was "is the marketplace_key.go embed unused on disk?" Answer: the embed is wired correctly. When the file is empty (this checkout, dev builds), boot logs the degradation and proceeds — operators get a clear "marketplace install requires third-party trust add" UX. When a release pipeline populates the file at release-time, the same code path adds the embedded key to the trust store.

**Recommended remediation:** none. Already correct.

The note exists to record the verification: the empty file is intentional for `release/0.8.0` because no marketplace signing infrastructure is online yet. Production release builds populate this file (see #384 milestone row in §12).

---

### SEC-080-010 (LOW) — Deprecation helper warns for any non-empty `ALF_EXPERIMENTAL` value (including `=0`)

**Severity:** LOW (note — operator confusion at boot)

**Evidence:**

- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/experimental.go:27-35`:
  ```go
  func warnDeprecatedExperimentalEnv(getenv func(string) string) {
      if getenv("ALF_EXPERIMENTAL") == "" {
          return
      }
      log.Printf("[boot] DEPRECATED: ALF_EXPERIMENTAL is set ...")
  }
  ```

The helper warns on every non-empty value, including `ALF_EXPERIMENTAL=0`. An operator who explicitly disabled the variable (set `=0` rather than removing the line) sees a deprecation warning saying the variable "is set". The intent of the warning is "you have it set to 1 from the dev window" but the code matches any non-empty value.

This is the example failure mode the user explicitly cited. It is harmless (boot proceeds) but the message is misleading: an operator following the cleanup advice ("remove the variable") may have already set `=0`, in which case the warning still fires and the cleanup feels like it didn't work.

**Recommended remediation:**

Match only truthy values:

```go
func warnDeprecatedExperimentalEnv(getenv func(string) string) {
    v := getenv("ALF_EXPERIMENTAL")
    if v == "" || v == "0" || strings.EqualFold(v, "false") {
        return
    }
    log.Printf("[boot] DEPRECATED: ALF_EXPERIMENTAL=%q is set but no longer used. ...", v)
}
```

Echoing the actual value in the message (`%q`) also helps the operator find the line in their config.

**Compliance impact:** none (UX only).

---

## Areas verified clean

For traceability, the following invariants and recent changes were checked and found to match the documented behaviour:

- **§3.1 Tier 3.1 forge unicity** — `TestMintRuntimeTokenIsRuntimeOnly` enforced; `MintRuntimeToken` calls confined to `internal/runtime/` and `internal/capability/handle/`. Confirmed by archtest source at `internal/archtest/capability_ocap_test.go:28-73`.
- **§7.4 unsigned bundles refused at every load entry** — `envelope.Verify` is the single chokepoint; archtest `TestOneVerifyCallSite` pins the two callers (`internal/runtime/instantiator_verified.go` + `internal/marketplace/bundle.go`). Both go through the full pipeline.
- **§7.4 marketplace verify is side-effect-free before signature check** — `internal/marketplace/manager.go:880-895::downloadAndVerifyBundle` reads bytes into memory only, calls `verifyBundle` BEFORE `extractVerifiedBundle`. `Update` correctly interposes the #402 permission diff between verify and extract.
- **§7.4 strict-before semantics for revocation** — `internal/capability/envelope/verify.go:139-146` rejects when `signed-at` is at or beyond `revoked-at`; equality rejects.
- **§3.3 events private-by-default** — `internal/runtime/instantiator_verified.go:147-170` only forges `EventSub` when `crossFlow.HasExport(fromID, s.Topic)` is true; missing exports silently drop the subscription per §3.3.
- **§4.2 invariant 1 (handles non-serializable)** — covered by `TestAllHandleTypesNonSerializable` archtest; behavioural per-handle MarshalJSON returning errors.
- **§4.2 invariant 5 (no unsafe / linkname / plugin in capability code)** — `TestNoUnsafeInCapabilityCode` + `TestHandlePackageNoUnsafeOrLinkname` + `TestNoPluginStdlibImport` all enforced.
- **§6 admin boundary** — `cmd/alf/admin/` package boundary enforced via `TestAdminCLIPackageBoundary`. Admin CLI does not import `internal/runtime` (`TestAdminCLIDoesNotImportRuntime`). Admin commands refuse non-TTY stdin.
- **§7.3 Tier 2 daemon key file mode** — `internal/runtime/wasm/daemonkey.go:119-128::enforcePerms` rejects on `mode & 0o077 != 0`; entrypoint creates `keys/` dir 0o700 owned by alfd. Named volume in docker-compose template prevents Docker Desktop fakeowner bypass.
- **#403 cosign + digest pin** — `Docker-Content-Digest` resolved via OCI HEAD before notify; `cosign verify` against pinned identity regex; verification failure aborts notify. Daemon production wiring sets the verifier unless `ALF_DISABLE_COSIGN_VERIFY=1`.
- **#86 cap drop** — `SYS_ADMIN` and `SYS_CHROOT` removed from `cap_add` in `internal/cli/templates/docker-compose.yml.tmpl:127-144`; verified zero remaining callers of `syscall.Mount`/`Chroot`/`Unshare`/`PivotRoot`. Ambient `CAP_SETUID`/`CAP_SETGID` dropped on LLM spawn via `setpriv --ambient-caps=-all --inh-caps=-all` at every of the 4 spawn sites (`cli.go`, `codex.go`, `classifier.go`, preflight).
- **#395 Stage 2 chunk 4** — `SecretValue` redacts via every standard formatter; `MarshalBinary` returns `ErrSecretValueNotMarshalable`. Vault user-scope structurally unreachable from any handle (Manager has no entry for `<dataDir>/keys/` or `<dataDir>/admin/`).
- **strict-flip retirement** — `WithExperimentalHeader` removed from `internal/controlcenter/server.go`; `experimental.go` and `experimental_test.go` deleted from controlcenter; the only remaining references are comments in code that document the retirement, plus the log strings called out in SEC-080-008.

---

## Summary table

| ID | Severity | Title | File:line |
|---|---|---|---|
| SEC-080-001 | HIGH (block) | Revocation cascade TOCTOU between Verify and trackLive | `internal/runtime/instantiator_verified.go:91-196`, `internal/runtime/cascade.go:104-125` |
| SEC-080-002 | HIGH (block) | Residual TOCTOU on top of SEC-407-001 fix (Lstat→Chmod) | `internal/runtime/agents/orchestrator.go:288-311, 902-922` |
| SEC-080-003 | HIGH (block) | nettrack-helper Unix sockets retain pre-#407 TOCTOU pattern | `cmd/nettrack-helper/main.go:83-92, 127-137` |
| SEC-080-004 | MEDIUM | Updater fail-open default + misleading operator log | `internal/platform/updater/checker.go:183-197` |
| SEC-080-005 | MEDIUM | Kernel-prompt nonce fail-open on `crypto/rand` failure | `internal/runtime/llm/kernel_prompt.go:52-63`, `internal/ai/provider/kernel_inject.go:22-28` |
| SEC-080-006 | MEDIUM | `EnforceTier2Ceiling` does not gate `[[depends]]` / `[[raw_imports]]` / `[provider.exports]` | `internal/capability/envelope/ceiling.go:46-55` |
| SEC-080-007 | MEDIUM | AppArmor + seccomp profiles shipped but not activated by default | `internal/cli/templates/docker-compose.yml.tmpl:109-126` |
| SEC-080-008 | LOW | "experimental" wording in production log lines | `internal/sandbox/exec/exec.go:40, 74` |
| SEC-080-009 | LOW | `marketplace_pubkey.minisign` empty — verified intentional | `internal/capability/envelope/marketplace_pubkey.minisign` |
| SEC-080-010 | LOW | Deprecation helper warns on `ALF_EXPERIMENTAL=0` | `cmd/alf-daemon/experimental.go:27-35` |

---

## Path forward

**To unblock the tag:**

1. Land the SEC-080-001 fix (re-check inside `trackLive` is the cheapest pattern). Add the race test.
2. Land the SEC-080-002 fix at all three Lstat→Chmod sites in `orchestrator.go` (`O_NOFOLLOW` open + `f.Chmod`). Add the symlink-race test.
3. Land the SEC-080-003 migration in `cmd/nettrack-helper/main.go` (extend `socknet.ListenUnix0660` to take a chown gid argument or accept the existing default and chown after; either is mechanical).

**Track post-tag (for v0.8.1 or v0.9.0):**

4. SEC-080-004 — make the updater fail closed by default; explicit opt-out via `ALF_DISABLE_COSIGN_VERIFY=1` only.
5. SEC-080-005 — fail loudly on `crypto/rand` failure; consolidate the two duplicate definitions.
6. SEC-080-006 — extend `EnforceTier2Ceiling` to refuse `Kind = capability-provider` + non-`alf:` depends + raw_imports.
7. SEC-080-007 — flip the AppArmor + seccomp default after soak. Or document the actual posture explicitly in §2.1.

**Cosmetic:**

8. SEC-080-008/010 — log-line cleanup. SEC-080-009 — no action.

---

## Methodology and reading list

Files read in full or in load-bearing detail (absolute paths):

- `/Users/alessandrolamparelli/Dev/alf/docs/ARCHITECTURE-SECURITY.md` (1054 lines, all sections)
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/main.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/experimental.go`
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/experimental_test.go`
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/wasm.go`
- `/Users/alessandrolamparelli/Dev/alf/cmd/alf-daemon/revocation_cascade.go`
- `/Users/alessandrolamparelli/Dev/alf/cmd/nettrack-helper/main.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/cli/templates/docker-compose.yml.tmpl`
- `/Users/alessandrolamparelli/Dev/alf/internal/sandbox/exec/exec.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/sandbox/secrets/manager.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/instantiator.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/instantiator_verified.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/revocation.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/cascade.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/agents/orchestrator.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/wasm/loader.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/wasm/daemonkey.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/wasm/import_check.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/runtime/llm/kernel_prompt.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/verify.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/truststore.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/ceiling.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/marketplace_key.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/schema.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/capability/envelope/marketplace_pubkey.minisign`
- `/Users/alessandrolamparelli/Dev/alf/internal/marketplace/bundle.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/marketplace/manager.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/platform/updater/checker.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/platform/updater/cosign.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/platform/socknet/socknet.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/ai/provider/kernel_inject.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/ai/provider/caps_linux.go`
- `/Users/alessandrolamparelli/Dev/alf/internal/admin/userkey/userkey.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/admin/pending/dir.go` (relevant ranges)
- `/Users/alessandrolamparelli/Dev/alf/internal/archtest/capability_ocap_test.go`
- `/Users/alessandrolamparelli/Dev/alf/scripts/apparmor-alf.profile`
- `/Users/alessandrolamparelli/Dev/alf/scripts/seccomp-alf.json` (header)
- `/Users/alessandrolamparelli/Dev/alf/scripts/entrypoint.sh` (relevant ranges)

Greps used (key patterns):

- `ALF_EXPERIMENTAL` / `X-ALF-Experimental` / `WithExperimentalHeader` — strict-flip residue check
- `net.Listen.*"unix"` / `os.Chmod.*0o660` — TOCTOU socket pattern hunt
- `EnforceTier2Ceiling` — ceiling enforcement sites
- `verifyBundle` / `downloadAndVerifyBundle` / `extractVerifiedBundle` — marketplace verify flow
- `MarketplacePublicKey` / `marketplace_pubkey` — embed wiring
- `crypto/rand` / `rand.Read` — randomness fail-modes
- `Lstat` / `O_NOFOLLOW` / `fchmodat` — symlink-aware operations
- `capDropWrap` — LLM cap drop call sites
- `MintRuntimeToken` / `envelope.Verify` — archtest invariants
- `ALF_DISABLE` / `ALF_INSECURE` / `ALF_SKIP` / `ALF_BYPASS` — operator escape hatches inventory
- `git clone` / `exec.*git` — supply-chain-via-git surface
- `os.WriteFile` / `os.Chmod` in admin/sandbox paths — admin-domain file mutations
- `nettrack` — helper deployment + cap inventory

Attack vectors mentally walked:

- Unsigned bundle install at every entry point (Install, Update, marketplace, skill loader, WASM loader, provider) — every path converges on `envelope.Verify`.
- Tampered manifest / tampered .wasm — both detected via canonical-bytes hash + bundle-SHA256 in trusted comment.
- Algorithm substitution (envelope claims ed25519 but signature is RSA) — verifier dispatches on declared algorithm; dispatch table only knows ed25519-ph-blake2b512.
- Trust-store TOCTOU (revoke during install) — found SEC-080-001.
- Symlink swap on directories the LLM owns — found SEC-080-002.
- Open-mode race on Unix sockets — found SEC-080-003 (nettrack), confirmed clean elsewhere.
- Tier-2 ceiling bypass — found SEC-080-006 (latent, not exploitable today).
- Prompt-injection break-out via marker tag — bounded by per-Invoke nonce; found SEC-080-005 (fail-open on rand failure).
- MitM on auto-update — bounded by cosign verify against pinned OIDC identity; found SEC-080-004 (fail-open on missing verifier).
- LLM reading daemon-private files — DAC + named volumes + cap drop on spawn — verified clean.
- LLM writing into admin domain — multi-layered (DAC + SEC-407-001 fixes + AppArmor profile if activated) — found SEC-080-002 residual.
- Kill-switch toggle by LLM — found SEC-080-003.
- Kernel-prompt regression — covered by `TestKernelPromptIsImported` archtest + production wiring confirmed at `cmd/alf-daemon/main.go:562`.
- Memory handle smuggling — covered by `TestNoMemoryHandleType` archtest; structural property by absence.
- Cross-flow leak without declaration — `instantiator_verified.go:155-170` only forges EventSub when crossFlow confirms the export. Verified.
- Forge token exfil — `TestMintRuntimeTokenIsRuntimeOnly` archtest confines the mint call to `internal/runtime/` and `internal/capability/handle/`. Verified.

End of audit.
