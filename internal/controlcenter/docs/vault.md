---
category: Security
tags: vault, secrets, proxy, api, credentials, tokens
order: 7
---

# Vault

Store API credentials securely and let Alf use them without seeing the secrets.

## How it works

Vault is a built-in secrets manager that runs inside the Alf container. It encrypts all credentials at rest using AES-256-GCM with a master password. When Alf needs to call an API, it uses the `vault` tool which proxies the request through vault-server - the credentials are injected server-side and never exposed to the AI.

**Architecture:**
- `vault-server` - background process managing encrypted storage + HTTP proxy
- `vault` CLI tool - available to Alf via `tools.d/` for proxied API calls
- Control Center vault page - admin UI for unlock, services CRUD, and token management

## Setup

### First-time setup via Control Center

1. Open the **Vault** tab in the sidebar
2. Choose a master password (min 8 characters) and click **Create Vault**
3. The vault is created, unlocked, and the password is persisted automatically

The master password is saved so the vault auto-unlocks on every container restart. No manual Docker secret configuration needed.

### Alternative: Set password via CLI (on host)

```bash
alf secret set vault_master_password "your-strong-password"
alf restart
```

Both methods achieve the same result. The Control Center method is recommended for simplicity.

### Add secrets (API keys)

The **Secrets** section stores key-value pairs like API keys and tokens. All backend API keys and Telegram credentials are stored here.

1. Open the **Vault** tab
2. Click **Add** in the Secrets section
3. Enter the name (e.g. `openrouter_api_key`) and value
4. Click **Save**

Naming convention for backend API keys: `<backend_name>_api_key` (e.g. `openrouter_api_key`, `openai_api_key`).

Other secrets stored in the vault:
- `telegram_bot_token` - Telegram bot authentication
- `telegram_chat_id` - Telegram chat identifier

> **Note:** All credentials (API keys, Telegram tokens) are stored exclusively in the vault. Docker secrets are only used for infrastructure bootstrap (`vault_master_password`, `cc_auth_token`).

### Add services

1. Open the **Vault** tab in the sidebar
2. Click **Add** to register a service (e.g., GitHub API, Slack, etc.)
3. Fill in the base URL and authentication credentials
4. Optionally check **Skip TLS verification** for internal services with self-signed certificates or HTTP endpoints
5. Click **Test** to verify connectivity

### Edit services

Click the pencil icon next to any service to update its configuration. The service name cannot be changed - update the base URL, auth credentials, or TLS settings as needed. Leave password fields empty to keep existing credentials.

### Alf uses the vault

Alf calls any registered service through the vault proxy:

```bash
vault proxy github GET /user
vault proxy slack POST /chat.postMessage '{"channel":"#general","text":"hello"}'
```

The `vault` tool automatically injects the right authentication headers. Alf never sees the actual API keys.

## Auth types

| Type | Fields | Description |
|------|--------|-------------|
| `bearer` | Token | Static `Authorization: Bearer <token>` header |
| `header` | Header name + value | Custom header injection (e.g. `X-API-Key`) |
| `basic` | Username + password | `Authorization: Basic <base64>` header |
| `oauth2_client` | 3 setup modes (see below) | OAuth2 with automatic token refresh |
| `service_account` | Service account key file | Google service account JWT exchange, no user consent needed |

### OAuth2 Client

Three setup modes, same `oauth2_client` type:

| Mode | What you need | Best for |
|------|---------------|----------|
| **Browser flow** | `client_secret_*.json` only | Simplest - no token.json needed |
| **File-based** | `client_secret_*.json` + `token.json` | When you already have a token.json |
| **Manual** | client_id, client_secret, refresh_token, token_url | Any OAuth2 provider |

**Browser flow (recommended for Google APIs):**

Prerequisites - in Google Cloud Console:
1. Create an OAuth Client ID of type **Web application** (not "Desktop app")
2. Add `https://<your-cc-domain>/api/vault/oauth2/callback` as an **Authorized redirect URI**
3. Download the `client_secret_*.json` file

Then in the Control Center:
1. Upload the `client_secret_*.json` via the **Files** section in the Vault tab
2. Select **OAuth2 Client** auth type, switch to **Browser Flow** tab
3. Fill in service name, base URL, select the uploaded file, add scopes
4. Click **Authorize in Browser** → a new tab opens with Google consent
5. Authorize → Google redirects back to the Control Center, which creates the service automatically

The flow expires after 5 minutes. The callback is protected by a 128-bit random state parameter.

**File-based:** Upload `client_secret_*.json` and `token.json` via the **Files** section, then create a service with type `oauth2_client` and fields `client_secret_file` + `token_file`. The vault extracts credentials automatically.

**Manual:** Provide `client_id`, `client_secret`, `refresh_token`, and `token_url` directly.

All three paths result in the same runtime behavior: the vault refreshes the access token 30 seconds before expiry and persists rotated refresh tokens.

### Service Account (Google, no user consent)

Upload a Google service account key JSON file, then create a service referencing it with `file_ref`. The vault signs JWTs and exchanges them for access tokens automatically.

> For detailed setup instructions, curl examples, and file format specs, see the [vault-proxy AUTH_SETUP guide](https://github.com/alamparelli/vault-proxy/blob/main/docs/AUTH_SETUP.md).

## Tokens

Vault uses scoped tokens for access control:

- **Admin** - full access (unlock, lock, service CRUD, token management). Used by the Control Center.
- **Proxy** - read-only, can only list services and proxy requests. This is what Alf gets.

Alf's `VAULT_TOKEN` environment variable contains a proxy-scoped token, so it cannot modify services, create tokens, or lock/unlock the vault. Tokens have a 1-year TTL and are automatically re-created if vault-server restarts.

## Security model

- Credentials are encrypted at rest in `vault-data/vault.enc` using AES-256-GCM
- Master password derives the encryption key via Argon2id
- Alf subprocess only receives `VAULT_ADDR` and `VAULT_TOKEN` (proxy scope)
- `vault-data/` volume is separate from the data directory - Alf cannot access the encrypted file
- SSRF protection: vault-server blocks requests to private/link-local IP ranges (DNS-level validation, ignores system HTTP proxy)
- TLS skip verify: allows HTTP and private IPs only when explicitly enabled per service (still blocks link-local/metadata IPs)

## Locking

Locking the vault immediately revokes all tokens and disables API proxy access. Alf will no longer be able to call external APIs until you unlock again. Scheduled jobs that use vault services will fail.

Use lock only when you need to immediately cut off all API access (e.g., compromised credentials). Under normal operation, the vault should stay unlocked.

## Export / Import

Back up all vault secrets before a reset or migration.

**Export:**
1. Open the **Vault** tab
2. Click **Export** in the toolbar → downloads `vault-export.json`

The export file contains all secret names and values in plain text. Keep it secure and delete after use.

**Import:**
1. Click **Import** in the toolbar
2. Select a `vault-export.json` file
3. Confirm → all secrets are restored (existing secrets with the same name are overwritten)

This is useful when resetting the vault password or migrating to a new instance.

## Reset vault

The master password is the encryption key - it cannot be changed. To start fresh with a new password:

**Via Control Center:**
1. Go to the **Vault** tab
2. **Export** your secrets first (see above)
3. Click **Reset** - this deletes all stored credentials and the persisted password
4. Choose a new master password
5. **Import** your secrets back

> **Warning:** Resetting deletes all stored API credentials. Export first to avoid data loss.

## Lifecycle

- **Container start:** vault-server starts automatically. If a master password is persisted (from a previous CC unlock or Docker secret), the vault auto-unlocks and creates proxy tokens.
- **Crash recovery:** vault-server restarts automatically with exponential backoff (1s → 30s max). After restart, it re-unlocks and re-creates all tokens automatically - no manual intervention needed.
- **CC unlock:** persists the master password so future restarts auto-unlock.
- **CC lock:** revokes all tokens, clears `VAULT_TOKEN` from environment. The persisted password is kept so you can unlock again easily.
- **CC reset:** deletes `vault.enc`, clears persisted password, restarts vault-server fresh.

## Troubleshooting

**Vault shows "Unreachable" in CC:**
Check daemon logs for `[vault]` entries. The process may have crashed. It should auto-restart within 30s.

**Alf says "vault: command not found":**
The `vault` symlink in `tools.d/` may be missing. Check `/opt/alf/tools.d/vault` exists and points to `/opt/alf/bin/vault-cli`.

**"invalid token" from vault proxy:**
Vault-server stores tokens in memory. If it restarted, all tokens were invalidated. The watchdog should re-create them automatically. Check logs for `[vault] re-authenticated after restart`. If not present, unlock the vault via CC.

**"No VAULT_TOKEN found" in scheduled jobs:**
The vault must be unlocked for Alf to use it. Check vault status in CC. If locked, unlock it - the token propagates to all future subprocess invocations immediately.

**Scheduled jobs fail after container restart:**
The master password may not be persisted. Unlock the vault once via the Control Center - the password is saved automatically for future restarts.
