# http-hello — #421 Wave 2 smoke test

Minimal WASM tool exercising the `alf_http_request` host import. Used
to validate the end-to-end pipeline of #421 Wave 2 (schema → forge →
host import → outbound HTTP via firewall) before migrating real apps
like `bookshelf`.

## What it does

- Reads input JSON `{"url": "https://httpbin.org/get"}` from the host
- Issues a `GET` through `alf_http_request`
- Returns `{"status": 200, "body": "..."}` or `{"error": "..."}`

## Why it's NOT in `skills.d/wasm/`

The auto-loader (`internal/runtime/wasm/loader.go`) signs unsigned
manifests in `skills.d/wasm/<id>/` with the Tier-2 local daemon key.
Manifests declaring `[[http.scopes]]` exceed the Tier-2 ceiling
(`SEC-080-006` lockdown), so an auto-sign of `http-hello` would
fail with `ErrCeilingExceeded`. Tier 3 (user-endorsed key) is the
only path that signs the bundle, and the operator must do it
explicitly via `alf keygen` + `alf sign`.

## Smoke-test on homelab

```bash
# 1. Build the WASM binary locally (or via dev-deploy).
cd technical/poc/http-hello/src
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm \
  go build -buildmode=c-shared -trimpath -o ../http-hello.wasm .

# 2. Push the bundle to homelab.
scp -r technical/poc/http-hello alessandro@192.168.129.101:/home/alessandro/alf2/data/wasm/

# 3. On the homelab (one-time keygen if missing).
ssh alessandro@192.168.129.101
alf keygen --comment "homelab user-endorsed"
# Enter passphrase twice.

# 4. Sign the bundle with the Tier 3 key.
cd /home/alessandro/alf2/data/wasm/http-hello
alf sign .
# Confirms: kind=wasm-tool, id=http-hello, http.scopes=[httpbin.org]
# Enter passphrase, produces manifest.sig

# 5. Restart the daemon (or hot-reload).
sudo systemctl restart alf-daemon  # or whatever the systemd unit is
# OR: docker compose -f /home/alessandro/alf2/docker-compose.yml restart

# 6. Tail the daemon logs and check for:
#    [wasm-loader] http-hello: bundle loaded (verified <fp>)
journalctl -u alf-daemon -f | grep -i http-hello

# 7. Invoke the tool (CLI path — depends on operator's setup):
alf tool invoke http-hello '{"url":"https://httpbin.org/get"}'
# Expected: {"status":200,"body":"{\"args\":{},...,\"origin\":\"...\"}"}
```

## Out-of-scope test

To verify the scope gate rejects unauthorised hosts, try a URL with
a different host:

```bash
alf tool invoke http-hello '{"url":"https://api.example.com/x"}'
# Expected: {"error":"out_of_scope"}
```

## Firewall log

Every request also lands in the firewall request log at
`/admin/firewall` (the CC). Operator can verify each WASM-originated
HTTP call shows up alongside everything else the daemon emits.
