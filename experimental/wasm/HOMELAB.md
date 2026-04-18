# Deploy & test the WASM runtime on your homelab

Scope: after deploying `spike/wasm` to the homelab, place one WASM tool
and one WASM app inside the running container via SSH, and verify both
work alongside the legacy subprocess sandbox (which is still active —
see `experimental/wasm/DELETIONS.md`).

## What's already in the binary

After `scripts/dev-deploy.sh` ships a fresh image, the daemon has:

- `internal/runtime/wasm` — wazero-backed runtime with compile cache
- 1 **bundled tool** (`wasm-demo`) embedded via `go:embed` — proves the
  pattern works with zero container-side setup
- Dynamic **discovery** on startup: scans `/home/alf/data/wasm-tools/*`
  and `/home/alf/data/wasm-apps/*` for user-placed manifests
- A dedicated HTTP listener on **127.0.0.1:8788** serving
  `/wasm-app/<name>/` for WASM apps

The legacy sandbox, integrity guard, chroot-in-bash subprocess pipeline
and the LLM's existing tools are **untouched**. Zero regression risk.

---

## After deploy — confirm the bundled tool is alive

```bash
# from your dev machine
ssh alessandro@192.168.129.101

# inside the homelab host
docker logs alf 2>&1 | grep -i wasm | head -10
```

Expected:

```
[wasm] registered tool "wasm-demo" (from /home/alf/data/wasm-bundled/tool-demo/manifest.toml)
[wasm] discovery: 1 tool(s), 0 app(s) registered
[wasm] app router listening on http://127.0.0.1:8788/wasm-app/
```

Ask Claude in a tier with tool access: "use the wasm-demo tool with
input 'hello'". In the daemon logs you should see:

```
[wasm:wasm-demo] info: wasm-demo invoked
[wasm:wasm-demo] info: wasm-demo done (run 1)
```

The tool's JSON response is passed back to Claude verbatim.

---

## Add a WASM tool via SSH (the "LLM-would-create" case)

On your dev machine, build a .wasm file. Easiest: reuse the notes
example from `experimental/wasm/examples/tool-hello/`, or write your own.

```bash
# build the example tool (outputs to experimental/wasm/bin/)
cd experimental/wasm
make build-examples
ls bin/tool-hello.wasm   # ~2.8 MB
```

Ship it to the container:

```bash
# from your dev machine
docker_host=alessandro@192.168.129.101

# 1. scp to the host
scp experimental/wasm/bin/tool-hello.wasm $docker_host:/tmp/
scp experimental/wasm/examples/tool-hello/manifest.toml $docker_host:/tmp/

# 2. copy into the container (docker cp or volume mount, depending on setup)
ssh $docker_host <<'EOF'
  docker exec alf mkdir -p /home/alf/data/wasm-tools/hello
  docker cp /tmp/tool-hello.wasm  alf:/home/alf/data/wasm-tools/hello/hello.wasm
  docker cp /tmp/manifest.toml    alf:/home/alf/data/wasm-tools/hello/manifest.toml
  # Fix ownership inside the container so alfd can read it:
  docker exec alf chown -R alf:alf /home/alf/data/wasm-tools/hello
EOF
```

Adjust `manifest.toml` inside the container so `entry` matches:

```bash
docker exec alf sed -i 's/^entry = ".*"/entry = "hello.wasm"/' \
  /home/alf/data/wasm-tools/hello/manifest.toml
docker exec alf sed -i 's/^name = ".*"/name = "hello"/' \
  /home/alf/data/wasm-tools/hello/manifest.toml
```

Restart the daemon so discovery picks it up (hot reload is Phase-4
territory, not in this spike):

```bash
docker restart alf
sleep 3
docker logs --tail=20 alf 2>&1 | grep wasm
```

Expected new line:

```
[wasm] registered tool "hello" (from /home/alf/data/wasm-tools/hello/manifest.toml)
[wasm] discovery: 2 tool(s), 0 app(s) registered
```

Ask Claude: "use the hello tool". You should see
`[wasm:hello] info: tool-hello starting` in logs.

---

## Add a WASM app via SSH

Same idea with `experimental/wasm/examples/app-hello/`, which also has
a frontend:

```bash
scp experimental/wasm/bin/app-hello.wasm $docker_host:/tmp/
scp experimental/wasm/examples/app-hello/manifest.toml $docker_host:/tmp/
scp -r experimental/wasm/examples/app-hello/frontend $docker_host:/tmp/frontend-playground

ssh $docker_host <<'EOF'
  docker exec alf mkdir -p /home/alf/data/wasm-apps/playground
  docker cp /tmp/app-hello.wasm        alf:/home/alf/data/wasm-apps/playground/playground.wasm
  docker cp /tmp/manifest.toml         alf:/home/alf/data/wasm-apps/playground/manifest.toml
  docker cp /tmp/frontend-playground/. alf:/home/alf/data/wasm-apps/playground/frontend/
  docker exec alf chown -R alf:alf /home/alf/data/wasm-apps/playground
EOF

# Fix name + entry inside the container
docker exec alf sh -c '
  sed -i "s/^entry = \".*\"/entry = \"playground.wasm\"/" \
         /home/alf/data/wasm-apps/playground/manifest.toml
  sed -i "s/^name = \".*\"/name = \"playground\"/" \
         /home/alf/data/wasm-apps/playground/manifest.toml
'

docker restart alf
```

Once the daemon is back up, the app is reachable at:

```
http://<homelab-ip>:8788/wasm-app/playground/
```

The CC listener on :8080 is **not** affected; this is a parallel port.
If you want to expose :8788 externally, map it in your docker-compose
or reverse-proxy config. For SSH tunnel test from your dev machine:

```bash
ssh -L 8788:127.0.0.1:8788 alessandro@192.168.129.101
# then open http://127.0.0.1:8788/wasm-app/playground/ in your browser
```

Click the buttons — `/api/btc` will hit coingecko (allowlisted in the
manifest), `/api/denied-demo` will return a clean `rc=-2` showing the
policy is enforced.

---

## Verify the three capabilities are live

```bash
# 1. Bundled tool (in the binary)
docker exec alf ls /home/alf/data/wasm-bundled/tool-demo/

# 2. User-placed tool
docker exec alf ls /home/alf/data/wasm-tools/

# 3. User-placed app
docker exec alf ls /home/alf/data/wasm-apps/

# 4. Discovery log
docker logs alf 2>&1 | grep "\[wasm\]" | tail -10
```

---

## What's still running from the legacy stack

Everything. This deploy adds the WASM runtime alongside. Your existing
Telegram bot, CC, tools, apps, scheduler, marketplace, vault, firewall,
integrity guard — all unchanged, all still active.

When you want to start dismantling the legacy sandbox, follow the phased
plan in `experimental/wasm/DELETIONS.md`. Each phase is a separate PR
with the build staying green at each step.

---

## Troubleshooting

**"wasm tools disabled"** in daemon log
: wazero init failed. Check `docker logs alf | grep wasm` for the exact
  error. Most likely: insufficient disk space for `/home/alf/data/wasm-data/`,
  or a permission issue on that path.

**"guest called a host capability not declared in manifest.permissions"**
: the manifest does not declare a host import the guest uses. Add it to
  `[permissions]` and restart.

**App at :8788 returns connection refused**
: either the daemon failed to register the WASM listener (check logs) or
  your homelab port isn't exposed. Use `ssh -L 8788:127.0.0.1:8788` from
  your dev machine for a quick tunnel test.

**Tool doesn't appear for Claude**
: verify the manifest parses (no TOML syntax errors), the `.wasm` file
  exists next to it, and the daemon log shows `[wasm] registered tool`
  at startup. If not, manifest parse failed and was silently skipped —
  grep for `[wasm-discovery]` in stderr.
