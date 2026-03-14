#!/usr/bin/env bash
set -euo pipefail

# Setup Wizard API — Integration Test Protocol
# Run after deploying to homelab: ./scripts/test-setup-wizard.sh

REMOTE_HOST="alessandro@192.168.129.101"
REMOTE_DIR="/home/alessandro/alf2"

# Resolve token — CC is internal to Docker network, use docker exec + curl
TOKEN=$(ssh "$REMOTE_HOST" "cat $REMOTE_DIR/secrets/cc_auth_token 2>/dev/null" || true)
if [ -z "$TOKEN" ]; then
  echo "FAIL: cannot read cc_auth_token"
  exit 1
fi
ALF="http://localhost:8080"

PASS=0
FAIL=0
SKIP=0

test_result() {
  local name="$1" expected="$2" actual="$3"
  if echo "$actual" | grep -q "$expected"; then
    echo "  PASS  $name"
    PASS=$((PASS + 1))
  else
    echo "  FAIL  $name"
    echo "        expected: $expected"
    echo "        actual:   $actual"
    FAIL=$((FAIL + 1))
  fi
}

api() {
  local method="$1" path="$2" body="${3:-}"
  if [ "$method" = "GET" ]; then
    ssh "$REMOTE_HOST" "docker exec alf curl -s -H 'Authorization: Bearer $TOKEN' '$ALF$path'"
  else
    # Write body to temp file inside container to avoid quoting hell
    ssh "$REMOTE_HOST" "echo '$body' | docker exec -i alf sh -c 'cat > /tmp/_body.json'"
    ssh "$REMOTE_HOST" "docker exec alf curl -s -X $method -H 'Authorization: Bearer $TOKEN' -H 'Content-Type: application/json' -d @/tmp/_body.json '$ALF$path'"
  fi
}

echo "=== Setup Wizard API Test Protocol ==="
echo "Target: $ALF"
echo ""

# --- 1. Status ---
echo "[1] GET /api/setup/status"
R=$(api GET /api/setup/status)
test_result "returns JSON" '"steps"' "$R"
test_result "has completed field" '"completed"' "$R"
echo ""

# --- 2. Presets (before creating any) ---
echo "[2] GET /api/setup/presets (empty)"
R=$(api GET /api/setup/presets)
test_result "returns presets object" '"presets"' "$R"
echo ""

# --- 3. Create test preset ---
echo "[3] Creating test preset on remote..."
ssh "$REMOTE_HOST" "mkdir -p $REMOTE_DIR/config.d/setup-presets"
ssh "$REMOTE_HOST" "cat > $REMOTE_DIR/config.d/setup-presets/claude-default.json" << 'PRESET'
{
  "id": "claude-default",
  "name": "Claude Default",
  "description": "Full Claude stack: Haiku, Sonnet, Opus",
  "backend": "claude",
  "router_config": {
    "router_model": "haiku",
    "router_backend": "",
    "default_fallback": "haiku",
    "router_distinctions": "Pick the tier that best balances capability and cost."
  },
  "tiers": [
    {"name": "haiku", "model": "haiku", "priority": 1, "enabled": true, "routable": true, "router_label": "Simple tasks", "max_turns": 30, "write_capable": true, "effort": "low", "force_command": true},
    {"name": "sonnet", "model": "sonnet", "priority": 2, "enabled": true, "routable": true, "router_label": "Code, debugging", "max_turns": 30, "write_capable": true, "effort": "medium", "force_command": true},
    {"name": "opus", "model": "opus", "priority": 3, "enabled": true, "routable": true, "router_label": "Architecture, reasoning", "max_turns": 40, "write_capable": true, "effort": "medium", "force_command": true}
  ]
}
PRESET
echo "  PASS  preset file created"
PASS=$((PASS + 1))
echo ""

# --- 4. Presets (with file) ---
echo "[4] GET /api/setup/presets (with preset)"
R=$(api GET /api/setup/presets)
test_result "has claude preset" '"claude-default"' "$R"
test_result "has backend=claude" '"claude"' "$R"
echo ""

# --- 5. Validation endpoints ---
echo "[5] Validation endpoints"

# 5a. Backend test with unreachable server
R=$(api POST /api/setup/backend/test '{"type":"custom","base_url":"http://127.0.0.1:1"}')
test_result "backend/test unreachable -> ok=false" '"ok":false' "$R"

# 5b. Backend test missing base_url
R=$(api POST /api/setup/backend/test '{"type":"custom"}')
test_result "backend/test no url -> 400" 'base_url is required' "$R"

# 5c. Claude check
R=$(api GET /api/setup/claude/check)
test_result "claude/check returns authenticated" '"authenticated"' "$R"

# 5d. Telegram validate with fake token
R=$(api POST /api/setup/telegram/validate '{"bot_token":"123:FAKE"}')
test_result "telegram/validate fake -> ok=false" '"ok":false' "$R"

# 5e. Telegram validate empty
R=$(api POST /api/setup/telegram/validate '{"bot_token":""}')
test_result "telegram/validate empty -> 400" 'bot_token is required' "$R"

# 5f. Ollama models (likely unreachable on homelab)
R=$(api GET "/api/setup/ollama/models?base_url=http://127.0.0.1:1")
test_result "ollama/models unreachable -> empty models" '"models":\[\]' "$R"
echo ""

# --- 6. Apply: preset only (safe, no vault needed) ---
echo "[6] POST /api/setup/apply (preset only)"
R=$(api POST /api/setup/apply '{"preset_id":"claude-default"}')
test_result "apply preset -> ok" '"ok":true' "$R"
test_result "no restart required" '"restart_required":false' "$R"
test_result "vault not unlocked" '"vault_unlocked":false' "$R"

# Verify tiers were written inside container
TIERS=$(ssh "$REMOTE_HOST" "docker exec alf cat /opt/alf/config.d/tiers.json 2>/dev/null")
test_result "tiers.json written" 'haiku' "$TIERS"
echo ""

# --- 7. Apply: Ollama backend (no vault needed) ---
echo "[7] POST /api/setup/apply (ollama backend)"
R=$(api POST /api/setup/apply '{"backends":{"ollama":{"base_url":"http://host.docker.internal:11434/v1"}},"timezone":"Europe/Brussels"}')
test_result "apply ollama -> ok" '"ok":true' "$R"

# Verify config was updated
CFG=$(ssh "$REMOTE_HOST" "docker exec alf cat /opt/alf/config.d/config.json 2>/dev/null || true")
test_result "config has ollama backend" 'ollama' "$CFG"
test_result "config has timezone" 'Europe/Brussels' "$CFG"
echo ""

# --- 8. Apply: API key without vault password ---
echo "[8] POST /api/setup/apply (api key, no vault pw)"
R=$(api POST /api/setup/apply '{"backends":{"openrouter":{"base_url":"https://openrouter.ai/api/v1","api_key":"sk-or-test"}}}')
# Should fail with 503 if vault is locked, or succeed if already unlocked
if echo "$R" | grep -q '"ok":true'; then
  echo "  PASS  apply with api_key (vault was already unlocked)"
  PASS=$((PASS + 1))
elif echo "$R" | grep -q 'vault'; then
  echo "  PASS  apply rejected: vault locked (expected)"
  PASS=$((PASS + 1))
else
  echo "  FAIL  unexpected response: $R"
  FAIL=$((FAIL + 1))
fi
echo ""

# --- 9. Apply: invalid preset ---
echo "[9] POST /api/setup/apply (invalid preset)"
R=$(api POST /api/setup/apply '{"preset_id":"nonexistent"}')
test_result "invalid preset -> error" 'not found' "$R"
echo ""

# --- 10. Apply: invalid timezone ---
echo "[10] POST /api/setup/apply (invalid timezone)"
R=$(api POST /api/setup/apply '{"timezone":"Invalid/Zone"}')
test_result "invalid timezone -> error" 'invalid timezone' "$R"
echo ""

# --- 11. Apply: empty body (no-op) ---
echo "[11] POST /api/setup/apply (empty body)"
R=$(api POST /api/setup/apply '{}')
test_result "empty apply -> ok" '"ok":true' "$R"
echo ""

# --- 12. Apply: idempotent ---
echo "[12] POST /api/setup/apply (idempotent - same preset twice)"
R=$(api POST /api/setup/apply '{"preset_id":"claude-default","timezone":"Europe/Brussels"}')
test_result "second apply -> ok" '"ok":true' "$R"
echo ""

# --- 13. Non-regression: chat still works ---
echo "[13] Non-regression checks"
R=$(api GET /api/status)
test_result "status endpoint works" '"version"' "$R"

R=$(api GET /api/tiers)
test_result "tiers endpoint works" '"tiers"' "$R"

R=$(api GET /api/setup/status)
test_result "setup status after apply" '"completed"' "$R"
echo ""

# --- Cleanup ---
echo "[cleanup] Removing test preset..."
ssh "$REMOTE_HOST" "rm -f $REMOTE_DIR/config.d/setup-presets/claude-default.json"
echo ""

# --- Summary ---
echo "================================"
echo "  PASS: $PASS"
echo "  FAIL: $FAIL"
echo "  SKIP: $SKIP"
echo "================================"
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
