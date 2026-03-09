---
category: Security
tags: vault, secrets, proxy, api, credentials, tokens
order: 7
---

# Vault

Store API credentials securely and let Claude use them without seeing the secrets.

## How it works

Vault is a built-in secrets manager that runs inside the ALF container. It encrypts all credentials at rest using AES-256-GCM with a master password. When Claude needs to call an API, it uses the `vault` tool which proxies the request through vault-server — the credentials are injected server-side and never exposed to the AI.

**Architecture:**
- `vault-server` — background process managing encrypted storage + HTTP proxy
- `vault` CLI tool — available to Claude via `tools.d/` for proxied API calls
- Control Center vault page — admin UI for unlock, services CRUD, and token management

## Setup

### Option A: First-time setup via Control Center

1. Open the **Vault** tab in the sidebar
2. Choose a master password (min 8 characters) and click **Create Vault**
3. The vault is created and unlocked immediately

> **Note:** This password won't persist across container restarts. For auto-unlock, also set the Docker secret (see Option B).

### Option B: Set a master password via CLI

On the host machine:

```bash
alf secret set vault_master_password "your-strong-password"
alf restart
```

The vault auto-unlocks at startup when `vault_master_password` is set as a Docker secret.

### Add services

1. Open the **Vault** tab in the sidebar
2. Click **Add** to register a service (e.g., GitHub API, Slack, etc.)
3. Fill in the base URL and authentication credentials
4. Click **Test** to verify connectivity

### Claude uses the vault

Claude can call any registered service through the vault proxy:

```bash
vault proxy github GET /user
vault proxy slack POST /chat.postMessage '{"channel":"#general","text":"hello"}'
```

The `vault` tool automatically injects the right authentication headers. Claude never sees the actual API keys.

## Auth types

| Type | Fields | Header injected |
|------|--------|----------------|
| `bearer` | Token | `Authorization: Bearer <token>` |
| `header` | Header name + value | `<HeaderName>: <value>` |
| `basic` | Username + password | `Authorization: Basic <base64>` |

## Tokens

Vault uses scoped tokens for access control:

- **Admin** — full access (unlock, lock, service CRUD, token management). Used by the Control Center.
- **Proxy** — read-only, can only list services and proxy requests. This is what Claude gets.

Claude's `VAULT_TOKEN` environment variable contains a proxy-scoped token, so it cannot modify services, create tokens, or lock/unlock the vault.

## Security model

- Credentials are encrypted at rest in `vault-data/vault.enc` using AES-256-GCM
- Master password derives the encryption key via Argon2id
- The master password itself is stored as a Docker secret (never in the container filesystem)
- Claude subprocess only receives `VAULT_ADDR` and `VAULT_TOKEN` (proxy scope)
- `vault-data/` volume is separate from the data directory — Claude cannot access the encrypted file
- SSRF protection: vault-server blocks requests to private IP ranges

## Reset vault

The master password is the encryption key — it cannot be changed. To start fresh with a new password:

**Via Control Center:**
1. Go to the **Vault** tab
2. If locked, click **Reset** next to the unlock form
3. Choose a new master password

**Via CLI (on host):**
```bash
ssh your-server 'rm /path/to/alf/vault-data/vault.enc'
alf secret set vault_master_password "new-password"
alf restart
```

> **Warning:** Resetting deletes all stored API credentials. Services must be re-added.

## Operational notes

- **First-time setup:** An empty vault is created automatically on first unlock
- **Crash recovery:** vault-server restarts automatically with exponential backoff
- **Without master password:** vault-server starts but stays locked. Unlock manually via the Control Center vault page
- **Disable vault:** Remove the `vault_master_password` secret. Vault-server still starts (locked) but has zero impact

## Troubleshooting

**Vault shows "Unreachable" in CC:**
Check daemon logs for `[vault]` entries. The process may have crashed. It should auto-restart within 30s.

**Claude says "vault: command not found":**
The `vault` symlink in `tools.d/` may be missing. Check `/opt/alf/tools.d/vault` exists and points to `/opt/alf/bin/vault-cli`.

**"HTTP 401" from vault proxy:**
The proxy token may have expired or the vault was re-locked. Check vault status in CC and unlock if needed.
