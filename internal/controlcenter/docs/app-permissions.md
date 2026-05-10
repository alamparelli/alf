# App Permissions & Sandbox Reference

This page is the source of truth for:

- Manifest permissions and what each grants
- How iframe apps and compiled apps reach the Control Center (CC) API
- Sandbox boundaries (filesystem, network, env)
- The per-app tools socket (`ALF_TOOLS_SOCK`) — compiled apps' primary channel to CC

See also `marketplace-apps.md` for manifest schema and `app-sdk.md` for SDK usage.

---

## Permission model

Declared in `manifest.json`:

```json
{
  "permissions": ["bash", "network"]
}
```

| Permission | Effect |
|---|---|
| `bash` | App may call `/api/bash`. Commands run in a sandbox scoped to the app's own data directory. |
| `network` | Sandbox (both bash and server) allows outbound network. Without this, egress is blocked. |
| `tool` | App may call `/api/tool` (structured tool invocation). `bash` and `tool` are independent. |
| `clipboard` | App may call `AlfSDK.clipboard.write/read`. |

Omitted permissions cause a **403** from CC. Permissions are checked server-side on every request — the client cannot self-grant.

For the full isolation model (the 3 layers, signing, and the kind decision tree), see [Isolation Model](docs:isolation-model).

---

## Two app shapes

### 1. Source-only apps (iframe-only)

HTML/JS served at `/apps/{slug}/`. Lives on the same origin as CC.

- SDK methods (`AlfSDK.bash`, `AlfSDK.api`, etc.) use the iframe's bearer token (issued via `/api/apps/{slug}/token`, refreshed every 4 minutes).
- Cross-origin concerns do not apply — requests are same-origin.
- No backend process.

### 2. Compiled apps (iframe + Go/other backend)

Same iframe as above, plus a long-running `server` process managed by the supervisor. The supervisor:

- Writes the chosen port to `data/port` inside the app dir.
- Reverse-proxies `/apps/{slug}/api/*` → `localhost:{port}/api/*`.
- Spawns the server inside the sandbox (see below).

The backend has **two independent channels** to CC:

**A. Via the iframe (`AlfSDK.bash`, etc.)**
The iframe runs JS that calls CC directly using its bearer token. For UI-initiated actions, prefer this path — permissions and auth are already wired.

**B. Via `ALF_TOOLS_SOCK` (this is the backend's main channel)**
The supervisor creates a per-app Unix socket at `<workDir>/tools.sock` and exports its path as `ALF_TOOLS_SOCK` in the server's env. Presence of the socket **is** the authentication — every request coming through it is tagged with the app's slug server-side.

From Go:

```go
tr := &http.Transport{
    DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return net.Dial("unix", os.Getenv("ALF_TOOLS_SOCK"))
    },
}
client := &http.Client{Transport: tr}
// Host is ignored for unix transport; any string works.
resp, _ := client.Post("http://tools/api/bash", "application/json",
    strings.NewReader(`{"command":"echo hi"}`))
```

The CLI tools in `PATH` (`llm`, `task`, `tier`, …) already use this socket when it is set — `exec.Command("llm", "haiku", "prompt")` from the backend works without any credential wiring.

---

## What `ALF_TOOLS_SOCK` exposes (per app)

Allowlist is identical to the daemon-wide tools socket, **plus** `/api/bash`:

- `/api/tasks`, `/api/tasks/chain`, `/api/teams`, `/api/skills/catalog`, `/api/tiers`
- `/api/config` (GET only)
- `/api/logs`, `/api/search`, `/api/llm/invoke`
- `/api/settings/avatar`
- `/api/bash` — **permission-checked** against the app's manifest
- `/api/apps/*` — scoped to your own slug

Blocked: `/api/marketplace/*`, `/api/developer/*`, arbitrary write endpoints. Apps must not install other apps or mutate global config.

Requests from the per-app socket carry:
- `X-Tools-Socket: 1` (auth bypass marker, Unix-socket only — stripped on TCP)
- `X-Tools-Socket-App: <slug>` (binds the request to the app identity)

`/api/bash` uses `X-Tools-Socket-App` as the slug for permission enforcement. No need to also send `app_slug` in the body.

---

## Sandbox boundaries

Applies to both bash (`/api/bash`) and server processes. For the full layered model see [Isolation Model](docs:isolation-model).

**Filesystem — what the app can touch:**
- Its own directory (read-write).
- Platform CLI tools in `PATH` (read-only) — this is how `llm`, `task`, `vault`, etc. resolve.
- System binaries and minimal devices needed to run a process.
- The vault proxy socket (`VAULT_PROXY_SOCK`) when the app has `network`.
- The per-app tools socket (`ALF_TOOLS_SOCK`).

**Filesystem — what the app cannot touch:** other apps, vault data directly, daemon config, other users' data, the daemon binary itself.

**Network:**
- Server processes need to listen on a port and inherit the container's network.
- Bash (`/api/bash`): gated by the `network` permission. Without it, DNS + egress are blocked.

**Identity:** the app runs as the daemon user (uid/gid 1000), with no ambient root.

**Environment** (what the server sees):
```
PATH=/opt/alf/tools.d:/usr/local/bin:/usr/bin:/bin
HOME=/home/alf
ALF_APP_DATA_DIR=<appDir>/data
ALF_TOOLS_SOCK=<appDir>/tools.sock       # set when supervisor has SetAppTools wired
VAULT_PROXY_SOCK=<appDir>/vault.sock     # set when app declares vault services
TMPDIR=/tmp
```
No daemon secrets, no `CC_AUTH_TOKEN`, no outgoing-token keys. The per-app socket is the only privilege the app holds.

---

## Pitfalls

- **"Command not found" from backend `exec.Command("llm", …)`** — `PATH` now includes `/opt/alf/tools.d`, so this resolves. If you still hit it, check that your binary inherits the supervisor's env (`os.Environ()` in Go passes it through automatically).

- **`/api/bash` returns 403 from the backend** — make sure `ALF_TOOLS_SOCK` is set and you're dialing it (not falling back to HTTPS `/api/bash` on a TCP port, which has no auth). Also: `"bash"` must be in `manifest.json` permissions.

- **`network unreachable` inside bash** — you need `"network"` in permissions. This is sandbox egress denial, not an auth problem.

- **"LLM generates text but doesn't execute"** — `llm` is text-only. For "reason + act" loops, have the LLM emit structured JSON and execute the action yourself from the backend. (Issue #296 tracks an agentic `--with-tools` mode.)

- **Sheet DOM isolated from handlers** — sheet HTML lives in the parent frame. Use `AlfSDK.querySheet(selector)` and `AlfSDK.patchSheet(selector, html)` from handlers instead of `document.querySelector`.
