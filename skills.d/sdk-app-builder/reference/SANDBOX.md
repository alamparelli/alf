# Sandbox & Vault Proxy — Reference

All app code (both `AlfSDK.bash()` and backend servers) runs inside a chroot jail.

---

## Sandbox constraints

- **DO** store data in the app's own `data/` directory
- **DO** use `AlfSDK.api()` or `AlfSDK.fetch()` to own REST proxy endpoints (`/apps/{slug}/api/...`)
- **DO** declare permissions in `manifest.json`
- **DO** declare vault `services` in `manifest.json` for external API access
- **DO** use `appsdk.NewVaultClient()` or `VAULT_PROXY_SOCK` for vault access
- **DO NOT** access `/home/alf/data/apps/other-app/` — other apps' directories are invisible
- **DO NOT** hardcode API keys or rely on `VAULT_TOKEN` — use the vault proxy

Apps needing data beyond their own directory should use the **REST proxy pattern**: the Go server exposes endpoints, the frontend fetches them, and the server accesses data within its sandbox only.

---

## Vault proxy (external API access)

Apps that need external API access (OpenRouter, Google APIs, etc.) declare services in `manifest.json`:

```json
{
  "services": ["openrouter", "google-api"]
}
```

The daemon creates a per-app vault proxy socket (`VAULT_PROXY_SOCK` env var) that only allows the declared services. The proxy injects authentication server-side — apps never see API keys or vault tokens.

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
