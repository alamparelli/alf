# Sandbox & Vault Proxy — Reference (0.8.0)

Apps built by this skill are **Go-kind** — maintainer-authored shells that run as supervised subprocesses inside the alf container. They are part of the daemon's TCB and run under the 0.8.0 isolation stack:

- **Layer 1 outer ring** — the alf container itself: `cap_drop: ALL` + narrow `cap_add` (CHOWN, DAC_OVERRIDE, FOWNER, NET_ADMIN, SETGID, SETUID), AppArmor `alf` profile, custom seccomp profile. See [`scripts/apparmor-alf.profile`](../../../scripts/apparmor-alf.profile) and [`scripts/seccomp-alf.json`](../../../scripts/seccomp-alf.json).
- **Layer 3 Tier 3.1** — for any external resource (vault, HTTP, fs outside the app dir, exec), your app receives object-capability handles forged from your manifest. You declare what you need; the daemon mints scoped handles at instantiation. See [`docs/ARCHITECTURE-SECURITY.md`](../../../docs/ARCHITECTURE-SECURITY.md) §3.

The pre-0.8.0 chroot+setpriv+bwrap path was razed in #406. There is no chroot jail.

> If you are writing a third-party tool (not a Go-kind app shell), stop and use the WASM-kind path instead: [`skills.d/wasm/SKILL.md`](../../wasm/SKILL.md). WASM-kind is mandatory for third-party / LLM-authored capabilities.

---

## What your app process sees

Inside the container, an app server process spawned by the supervisor sees:

- **Read-write:** `/home/alf/data/apps/<slug>/` — your app directory and everything under it (data/, frontend/, etc.).
- **Read-only:** system binaries (`/bin`, `/usr`, `/lib`, `/sbin`, `/lib64`), TLS CA certs, DNS resolver config.
- `/dev/{null,zero,urandom,random}`, `/proc`, private `/tmp`.
- **Not visible at the OCAP layer** (#391): other apps' directories. Even though the kernel filesystem still contains them, your app receives a forged `fs` handle scoped to `/home/alf/data/apps/<slug>/`. Reads/writes outside that scope go through the handle and are rejected.

---

## Sandbox conventions for app authors

- **DO** store data in your app's own `data/` directory.
- **DO** use `AlfSDK.api()` or `AlfSDK.fetch()` from the frontend to call your own REST proxy endpoints (`/apps/{slug}/api/...`).
- **DO** declare permissions in `manifest.json`. The daemon uses your declarations to forge ocap handles at startup.
- **DO** declare vault `services` in `manifest.json` for external API access.
- **DO** use `appsdk.NewVaultClient()` or `VAULT_PROXY_SOCK` for vault access.
- **DO NOT** access `/home/alf/data/apps/other-app/` — even if the path resolves, the ocap handle will refuse, and the AppArmor profile may reject the operation depending on the path.
- **DO NOT** hardcode API keys, tokens, or rely on `VAULT_TOKEN` — go through the vault proxy.
- **DO NOT** assume access to `/etc/passwd`, `/etc/shadow`, kernel modules, mount/umount, or namespace operations. The seccomp profile blocks dangerous syscalls (mount, umount, ptrace, init_module, kexec_*, perf_event_open, etc.) and the AppArmor profile denies CAP_SYS_ADMIN, CAP_SYS_CHROOT, CAP_SYS_MODULE, CAP_SYS_RAWIO.

Apps needing data beyond their own directory should use the **REST proxy pattern**: the Go server exposes endpoints, the frontend fetches them, and the server accesses data within its scope only.

---

## Vault proxy (external API access)

Vault access is gated by **per-app declarations in `manifest.json`** — this is how the Tier 3.1 forge knows what scope to mint:

```json
{
  "services": ["openrouter", "google-api"]
}
```

The daemon creates a per-app vault proxy socket (`VAULT_PROXY_SOCK` env var) that only allows the declared services. The proxy injects authentication server-side — apps never see API keys or vault tokens. Requests to undeclared services return 403.

**Go server apps** use `pkg/appsdk`:

```go
vc, _ := appsdk.NewVaultClient()
resp, err := vc.Proxy("openrouter", "POST", "/v1/chat/completions", body)
```

Or via the `App` helper:

```go
app := appsdk.New("my-app")
app.Vault().ProxyJSON("openrouter", "POST", "/v1/chat/completions", req, &resp)
```

> **Note:** `appsdk` is the Go-kind path. WASM-kind tools cannot link `appsdk` — they must declare permissions in their TOML envelope and receive forged handles via the `alf` host module ABI. See [`skills.d/wasm/SKILL.md`](../../wasm/SKILL.md).

---

## Sandbox failure modes (so you can recognise them)

When you write code that violates the isolation, you'll see one of these:

- `permission denied` from POSIX — you tried to access a path the AppArmor profile or POSIX permissions reject.
- `403` from the vault proxy — you called a service not in your `manifest.json`'s `services` list.
- `Operation not permitted` from a syscall — seccomp blocked it. Common offenders: trying to set thread affinity unusually, raw sockets, ptrace, mount.
- `apparmor="DENIED"` lines in the host's `dmesg` — AppArmor caught a path or syscall the profile forbids. Check the `operation`, `class`, and `denied_mask` fields to diagnose.

If your app legitimately needs a permission it's not getting, **declare it in `manifest.json`** and let the daemon forge the right handle. Do not work around the isolation — it is the contract.
