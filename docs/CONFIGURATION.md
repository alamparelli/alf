# Configuration

Runtime configuration for `alf-daemon`. Most values are environment
variables read at boot; a few are files written into `<dataDir>` by the
daemon itself. This page is a reference, not a tutorial — see the
README for the happy-path setup.

## Boot gates

| Variable | Required | Purpose |
|---|---|---|
| `ALF_EXPERIMENTAL=1` | yes (0.8.0 dev window) | Acknowledges this build has no legacy sandbox isolation. Daemon refuses to boot without it. Lifted when `ALF_OCAP_STRICT=1` replaces it (post-0.8.0). See [`cmd/alf-daemon/experimental.go`](../cmd/alf-daemon/experimental.go) and [#406](https://github.com/alamparelli/alf/issues/406). |

## Paths

| Variable | Default | Purpose |
|---|---|---|
| `ALF_DATA_DIR` | `/home/alf/data` | Daemon state: logs, sessions, vault, CRL cache |
| `ALF_CONFIG_DIR` | container path | Read-only configuration mount |
| `ALF_SKILLS_DIR` | container path | Skills + WASM bundle root |
| `ALF_HOME_DIR` | `/home/alf` | LLM subprocess home |
| `ALF_DIR`, `ALF_APP_DATA_DIR` | derived | Less-common path overrides |

## Authentication & networking

| Variable | Default | Purpose |
|---|---|---|
| `ALF_CC_BIND` | platform default | Control Center listen address |
| `ALF_CC_URL` | derived | External Control Center URL (used in magic links) |
| `ALF_CHAIN_ORIGIN` | derived | Allowed origin for cross-domain CC requests |
| `ALF_TOKEN` | from vault | Bearer token for CC API |
| `ALF_TRUSTED_PROXY_CIDRS` | empty | CIDRs whose `X-Forwarded-For` is honoured |
| `ALF_TOOLS_SOCK` | derived | Unix socket for tool subprocesses (replaces `CC_AUTH_TOKEN` env-passing) |
| `ALF_SIGNAL_SOCK` | derived | Telegram/signal IPC socket |

## Marketplace

| Variable | Default | Purpose |
|---|---|---|
| `ALF_MARKETPLACE_ENABLED` | `0` | Activates marketplace integration |
| `ALF_MARKETPLACE_URL` | placeholder | Marketplace HTTPS endpoint |
| `ALF_MARKETPLACE_INSECURE` | unset | Skips TLS verification (homelab only) |

## Trust store (#395 Stage 2 chunk 1)

The daemon's WASM trust store lives at `<dataDir>/trust/` — one
minisign `.pub` file per operator-trusted signing key, plus an
optional `.revoked` sidecar per key holding an RFC3339 not-valid-
after timestamp. Format details and rationale in
[§7.2](ARCHITECTURE-SECURITY.md#72-trust-store).

| Path | Owner | Purpose |
|---|---|---|
| `<dataDir>/trust/<keyid>.pub` | operator (via `alf trust add`) | minisign-format public key, mode 0o644 |
| `<dataDir>/trust/<keyid>.revoked` | operator (via `alf trust revoke`) | RFC3339Nano timestamp; `envelope.Verify` rejects bundles whose `signed-at >= timestamp` |
| `<dataDir>/keys/daemon.json` | daemon (auto-bootstrap) | local daemon keypair (Tier 2). Auto-trusted at boot, **not** listed under `<dataDir>/trust/` and not surfaced by `alf trust list`. |

The `alf trust` CLI mutates this directory directly without daemon
roundtrip — TTY-only, non-TTY stdin refused. Changes take effect on
the next `alf restart` (hot-reload via SIGHUP is a planned follow-up).
Full command surface and §6 admin-boundary rules in
[§7.6](ARCHITECTURE-SECURITY.md#76-admin-cli-surface).

## User-endorsed key (#395 Stage 2 chunk 2)

The §7.3 Tier-3 user-endorsed signing key is persisted at
`<dataDir>/keys/user-endorsed.json` (mode 0o600, parent 0o700) and
encrypted at rest with ChaCha20-Poly1305 under a 32-byte
argon2id-derived key (t=3, m=64MiB, p=4). Format details in
[§7.3](ARCHITECTURE-SECURITY.md#tier-3---user-endorsed-key-alf-keygen)
and [`internal/admin/userkey/userkey.go`](../internal/admin/userkey/userkey.go).

| Path | Owner | Purpose |
|---|---|---|
| `<dataDir>/keys/user-endorsed.json` | operator (via `alf keygen`) | passphrase-encrypted Ed25519 keypair (Tier 3). Decrypted only inside `userkey.Store.Sign` / `WithPrivateKey` callbacks; zeroed on return. |
| `<dataDir>/keys/daemon.json` | daemon (auto-bootstrap) | local daemon keypair (Tier 2). Listed here for layout completeness; not part of chunk 2. |

The `alf keygen` and `alf sign` commands mutate this file (and
bundle directories) directly — TTY-only, non-TTY stdin refused.
Lost passphrase = re-keygen with `--force`; bundles signed by the
old key will fail verification afterwards. Distribute the public
half across machines via `alf keygen --export-pub <path>` followed
by `alf trust add <path>` on each peer.

## Ratification queue (#395 Stage 2 chunk 3)

The admin-side ratification queue lives at
`<dataDir>/admin/pending/` — one JSON file per pending item (mode
0o600, parent 0o700). Format details in
[§7.6](ARCHITECTURE-SECURITY.md#76-admin-cli-surface) and
[`internal/admin/pending/dir.go`](../internal/admin/pending/dir.go).

| Path | Owner | Purpose |
|---|---|---|
| `<dataDir>/admin/pending/<id>.json` | daemon (via the `pending.Store.Append` path; CLI removes via `Approve`/`Deny`) | one ratification item — `Kind` enum (`trust.add`, `bundle.install`, `permission.widen`), narrow string-keyed payload, originating capability id |

The `alf pending` command is read-only and does not require a TTY.
`alf ratify <id> [--deny]` is mutating: refuses non-TTY stdin,
shows the full item details before the confirm prompt, removes the
item on approval. Removal from the queue does **not** itself
execute the requested operation — the consumer that `Append`'d the
item (Runtime's widening path, not yet wired) is responsible for
the side effect. The CLI only flips the gate.

CC `/admin/ratify/*` route is deferred (chunk 3.5 / CC follow-up).
For the 0.8.0-beta soak, the TTY-only CLI is the supported surface.

## Revocation (#396 Stage 2)

| Variable | Default | Purpose |
|---|---|---|
| `ALF_CRL_URL` | unset | HTTPS URL of the signed CRL JSON published by alf release infra. When unset, the CRL refresher is disabled and operator-set `Revoke()` becomes the only revocation channel. See [§7.7 / §8](ARCHITECTURE-SECURITY.md#77-revocation) and [`internal/capability/crl/`](../internal/capability/crl/). |

The CRL refresher additionally requires the alf release pubkey to be
embedded in the binary at build time (via `go:embed` on
[`internal/capability/envelope/release_pubkey.minisign`](../internal/capability/envelope/release_pubkey.minisign)).
On a fresh checkout that file is empty and the refresher logs once
then degrades to "operator-set `Revoke()` only". Maintainers populate
it via `go run ./cmd/alf-release-keygen` — see
[CONTRIBUTING.md](../CONTRIBUTING.md#crl-signing-maintainer-flow).

## Build-time injection

The daemon binary's build time is link-time-injected for clock-sanity
checks (§7.7):

```
go build -ldflags="-X github.com/alamparelli/alf/internal/capability/envelope.buildTime=2026-04-26T12:00:00Z" ./cmd/alf-daemon
```

When the system clock is more than 1 year before this stamp, the
daemon refuses to boot. Dev builds without the ldflag degrade to
no-op (see [`internal/capability/envelope/clocksanity.go`](../internal/capability/envelope/clocksanity.go)).

The Dockerfile already injects `-X main.version=${BUILD_VERSION}`;
adding `-X .../envelope.buildTime=...` is the planned slot at release
tag time.
