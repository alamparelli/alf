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
