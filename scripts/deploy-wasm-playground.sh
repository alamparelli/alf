#!/usr/bin/env bash
# Deploy the WASM playground app to the homelab container.
#
# Places four files under /home/alf/data/apps/wasm-playground/ :
#   manifest.json    → makes the app appear in the CC sidebar (marketplace format)
#   manifest.toml    → registers the app in the WASM runtime
#   index.html       → iframe content loaded when user clicks the sidebar entry
#   wasm-playground.wasm → compiled guest
#
# After placement, restarts the alf container so discovery + sidebar refresh.
set -euo pipefail

REMOTE=alessandro@192.168.129.101
SLUG=wasm-playground

cd "$(git rev-parse --show-toplevel)"

echo "==> [1/4] Building WASM backend (from app-hello example)"
cd experimental/wasm
make build-examples >/dev/null
WASM_BIN=$(pwd)/bin/app-hello.wasm
[ -f "$WASM_BIN" ] || { echo "ERR: $WASM_BIN not built"; exit 1; }
cd - >/dev/null

echo "==> [2/4] Preparing remote directory"
ssh "$REMOTE" "docker exec alf mkdir -p /home/alf/data/apps/${SLUG}"

echo "==> [3/4] Writing 4 files to container"

# Marketplace manifest — sidebar entry.
cat > /tmp/${SLUG}-manifest.json <<EOF
{
  "name": "WASM Playground",
  "slug": "${SLUG}",
  "version": "0.1.0",
  "description": "Demo app served by the ALF wazero runtime.",
  "category": "developer",
  "icon": "code",
  "permissions": ["storage"]
}
EOF

# WASM runtime manifest.
cat > /tmp/${SLUG}-manifest.toml <<EOF
name = "${SLUG}"
version = "0.1.0"
kind = "app"
runtime = "go-wasip1"
entry = "${SLUG}.wasm"
description = "WASM playground demo."

[permissions]
log     = true
storage = true
vault   = ["coingecko", "httpbin"]
EOF

# Static iframe content — works same-origin in the CC sidebar. Buttons that
# hit the WASM backend require a tunnel to port 8788 until the daemon's
# AppRouter is mounted inside the CC mux (follow-up work).
cat > /tmp/${SLUG}-index.html <<'HTMLEOF'
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>WASM Playground</title>
  <link rel="stylesheet" id="alf-theme" href="/static/theme-sage.css">
  <script src="/static/alf-app-sdk.js"></script>
  <style>
    body { font-family: system-ui; max-width: 720px; margin: 40px auto; padding: 0 20px; line-height: 1.5; }
    h1 { font-size: 1.4rem; }
    code { background: #f4f4f4; padding: 2px 6px; border-radius: 4px; font-size: 0.9em; }
    pre { background: #0f172a; color: #e2e8f0; padding: 14px; border-radius: 8px; overflow-x: auto; font-size: 13px; }
    button { background: #2563eb; color: white; border: 0; padding: 8px 14px; border-radius: 6px; cursor: pointer; margin: 4px 4px 4px 0; font-size: 14px; }
    button:hover { background: #1d4ed8; }
    .note { background: #fef3c7; padding: 12px; border-radius: 6px; font-size: 13px; border-left: 3px solid #f59e0b; margin: 20px 0; }
    .ok { color: #065f46; }
    .err { color: #991b1b; }
  </style>
</head>
<body>
  <h1>🧩 WASM Playground</h1>
  <p>
    This app is served by ALF's wazero runtime. The sidebar entry comes
    from <code>manifest.json</code>; the backend from <code>manifest.toml</code>
    + <code>wasm-playground.wasm</code> (a Go program compiled to
    <code>wasip1</code>).
  </p>

  <h2>Endpoints</h2>
  <p>
    <button onclick="call('/api/hello')">/api/hello</button>
    <button onclick="call('/api/counter')">/api/counter (storage)</button>
    <button onclick="call('/api/btc')">/api/btc (vault→coingecko)</button>
    <button onclick="call('/api/denied-demo')">/api/denied-demo (expect rc=-2)</button>
  </p>

  <pre id="out">response will appear here…</pre>

  <div class="note">
    The WASM backend is mounted inside the CC mux at
    <code>/wasm-app/wasm-playground/*</code>. Fetches from this iframe
    are same-origin and authenticated by your CC session cookie.
  </div>

  <h2>How it works</h2>
  <pre>CC sidebar ── iframe ── index.html ── fetch /wasm-app/…/api/*
                                              │
                                              ▼  (port 8788)
                                        wasm.AppRouter
                                              │
                                              ▼
                                        wazero instantiates
                                        wasm-playground.wasm
                                              │
                                              ├─ alf.LogInfo
                                              ├─ alf.Storage.Put/Get
                                              └─ alf.VaultRequest("coingecko")
                                         (manifest-gated)</pre>

  <script>
    // AlfSDK.init performs the MessageChannel handshake with the CC parent
    // to receive a Bearer app token. After that, AlfSDK.fetch attaches the
    // token automatically — required because the iframe is sandboxed
    // (Origin: null, no cookies).
    AlfSDK.init({ slug: 'wasm-playground' });

    async function call(path) {
      const out = document.getElementById('out');
      out.textContent = 'loading…';
      out.className = '';
      try {
        const r = await AlfSDK.fetch('/wasm-app/wasm-playground' + path);
        const t = await r.text();
        out.className = r.ok ? 'ok' : 'err';
        out.textContent = 'HTTP ' + r.status + ' ' + r.statusText + '\n\n' + t;
      } catch (e) {
        out.className = 'err';
        out.textContent = 'error: ' + (e.message || String(e));
      }
    }
  </script>
</body>
</html>
HTMLEOF

# Copy all four files in.
scp /tmp/${SLUG}-manifest.json   "$REMOTE:/tmp/${SLUG}-manifest.json"
scp /tmp/${SLUG}-manifest.toml   "$REMOTE:/tmp/${SLUG}-manifest.toml"
scp /tmp/${SLUG}-index.html      "$REMOTE:/tmp/${SLUG}-index.html"
scp "$WASM_BIN"                  "$REMOTE:/tmp/${SLUG}.wasm"

ssh "$REMOTE" bash <<EOF
set -e
docker cp /tmp/${SLUG}-manifest.json alf:/home/alf/data/apps/${SLUG}/manifest.json
docker cp /tmp/${SLUG}-manifest.toml alf:/home/alf/data/apps/${SLUG}/manifest.toml
docker cp /tmp/${SLUG}-index.html    alf:/home/alf/data/apps/${SLUG}/index.html
docker cp /tmp/${SLUG}.wasm          alf:/home/alf/data/apps/${SLUG}/${SLUG}.wasm
docker exec alf chown -R alf:alf /home/alf/data/apps/${SLUG}
echo "    placed:"
docker exec alf ls -la /home/alf/data/apps/${SLUG}/
EOF

echo "==> [4/4] Restarting alf container"
ssh "$REMOTE" "cd /home/alessandro/alf2 && docker compose restart alf" 2>&1 | tail -3

echo "==> Waiting 4s for daemon boot…"
sleep 4

echo ""
echo "==> Daemon log (wasm-related lines):"
ssh "$REMOTE" "docker logs --tail=40 alf 2>&1 | grep -iE '(wasm|discovery|playground)' | tail -10"

echo ""
echo "==> Done."
echo "   Open the CC sidebar → 'WASM Playground' under 'developer'."
echo "   Clicking the buttons hits the WASM backend via /wasm-app/${SLUG}/api/*"
echo "   — same-origin, no tunnel needed."
