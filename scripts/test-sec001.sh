#!/usr/bin/env bash
set -euo pipefail

# SEC-001 Verification Protocol
# Run AFTER deploying the new image (e.g. after dev-deploy.sh).
# Usage: ./scripts/test-sec001.sh [--remote user@host] [--dir /path/to/alf]

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

REMOTE=""
ALF_DIR=""
CONTAINER="alf"
PASSED=0
FAILED=0
SKIPPED=0

for arg in "$@"; do
  case "$arg" in
    --remote=*) REMOTE="${arg#*=}" ;;
    --dir=*)    ALF_DIR="${arg#*=}" ;;
  esac
done

# Execute a command on the host (local or remote).
hostcmd() {
  local cmd="$1"
  if [ -n "$REMOTE" ]; then
    ssh "$REMOTE" "$cmd"
  else
    eval "$cmd"
  fi
}

# Execute a command inside the container.
# Uses printf %q to safely quote the inner command through SSH.
dexec() {
  local escaped
  escaped=$(printf '%q' "$1")
  hostcmd "docker exec $CONTAINER sh -c $escaped"
}

# Execute a command inside the container as a specific user.
dexec_as() {
  local user="$1"
  local escaped
  escaped=$(printf '%q' "$2")
  hostcmd "docker exec -u $user $CONTAINER sh -c $escaped"
}

pass() { echo -e "  ${GREEN}PASS${NC} $1"; PASSED=$((PASSED + 1)); }
fail() { echo -e "  ${RED}FAIL${NC} $1"; FAILED=$((FAILED + 1)); }
skip() { echo -e "  ${YELLOW}SKIP${NC} $1"; SKIPPED=$((SKIPPED + 1)); }

assert_contains() {
  local output="$1" expected="$2" label="$3"
  if echo "$output" | grep -q "$expected"; then
    pass "$label"
  else
    fail "$label (expected '$expected' in output)"
    echo "       got: $(echo "$output" | head -3)"
  fi
}

assert_not_contains() {
  local output="$1" rejected="$2" label="$3"
  if echo "$output" | grep -qi "$rejected"; then
    fail "$label (found '$rejected' in output)"
    echo "       got: $(echo "$output" | head -3)"
  else
    pass "$label"
  fi
}

assert_equals() {
  local actual="$1" expected="$2" label="$3"
  if [ "$actual" = "$expected" ]; then
    pass "$label"
  else
    fail "$label (expected '$expected', got '$actual')"
  fi
}

echo "============================================"
echo " SEC-001 Verification Protocol"
echo " Drop daemon from root to uid 1000 (alf)"
echo "============================================"
echo ""

# Pre-flight: container must be running.
echo "[0] Pre-flight checks"
if ! hostcmd "docker ps --format '{{.Names}}' | grep -q '^${CONTAINER}$'" 2>/dev/null; then
  echo -e "  ${RED}ABORT${NC} Container '$CONTAINER' is not running."
  echo "  Deploy first with: ./scripts/dev-deploy.sh"
  exit 1
fi
pass "Container '$CONTAINER' is running"
echo ""

# -------------------------------------------------------
echo "[1] Dockerfile: uid 1001 user removed"
PASSWD=$(dexec "cat /etc/passwd" 2>/dev/null || echo "")
assert_not_contains "$PASSWD" "claude" "No 'claude' user in /etc/passwd"
assert_contains "$PASSWD" "alf" "User 'alf' (uid 1000) exists"
echo ""

# -------------------------------------------------------
echo "[2] Daemon process identity"
PS_OUT=$(dexec "ps -o user,pid,comm -p 1 --no-headers" 2>/dev/null || echo "")
assert_contains "$PS_OUT" "alf" "PID 1 runs as user 'alf'"
assert_not_contains "$PS_OUT" "root" "PID 1 is NOT root"

# Cross-check via /proc (more reliable than ps).
PROC_UID=$(dexec "awk '/^Uid:/{print \$2}' /proc/1/status" 2>/dev/null || echo "unknown")
assert_equals "$PROC_UID" "1000" "PID 1 real UID = 1000 (via /proc)"
echo ""

# -------------------------------------------------------
echo "[3] Effective capabilities = zero"
CAP_EFF=$(dexec "awk '/^CapEff:/{print \$2}' /proc/1/status" 2>/dev/null || echo "unknown")
assert_equals "$CAP_EFF" "0000000000000000" "CapEff is 0 (zero capabilities)"

CAP_PRM=$(dexec "awk '/^CapPrm:/{print \$2}' /proc/1/status" 2>/dev/null || echo "unknown")
assert_equals "$CAP_PRM" "0000000000000000" "CapPrm is 0 (no permitted capabilities)"

CAP_INH=$(dexec "awk '/^CapInh:/{print \$2}' /proc/1/status" 2>/dev/null || echo "unknown")
assert_equals "$CAP_INH" "0000000000000000" "CapInh is 0 (no inheritable capabilities)"
echo ""

# -------------------------------------------------------
echo "[4] Daemon user context (as uid 1000)"
ID_OUT=$(dexec_as 1000 "id" 2>/dev/null || echo "")
assert_contains "$ID_OUT" "uid=1000" "id shows uid=1000"
assert_contains "$ID_OUT" "gid=1000" "id shows gid=1000"
echo ""

# -------------------------------------------------------
echo "[5] Data directory permissions"
# Use test -w (writable check) instead of touch+rm to avoid hook issues.
WRITE_TEST=$(dexec_as 1000 "test -w /home/alf/data && echo ok" 2>/dev/null || echo "FAIL")
assert_equals "$WRITE_TEST" "ok" "uid 1000 can write to /home/alf/data/"

READ_TEST=$(dexec_as 1000 "ls /opt/alf/config.d/ >/dev/null 2>&1 && echo ok" 2>/dev/null || echo "FAIL")
assert_equals "$READ_TEST" "ok" "uid 1000 can read /opt/alf/config.d/"

VAULT_TEST=$(dexec_as 1000 "test -w /opt/alf/vault-data && echo ok" 2>/dev/null || echo "FAIL")
assert_equals "$VAULT_TEST" "ok" "uid 1000 can write to /opt/alf/vault-data/"

HOME_TEST=$(dexec_as 1000 "test -w /home/alf/.claude && echo ok" 2>/dev/null || echo "FAIL")
assert_equals "$HOME_TEST" "ok" "uid 1000 can write to ~/.claude/"
echo ""

# -------------------------------------------------------
echo "[6] Socket files accessible"
SOCK_FILE=$(dexec "find /home/alf/data -name '*.sock' -type s 2>/dev/null | head -1" || echo "")
if [ -n "$SOCK_FILE" ]; then
  SOCK_OWNER=$(dexec "stat -c %u $SOCK_FILE" 2>/dev/null || echo "unknown")
  assert_equals "$SOCK_OWNER" "1000" "Socket owned by uid 1000"
else
  skip "No socket files found (daemon may not have created them yet)"
fi
echo ""

# -------------------------------------------------------
echo "[7] Environment sanitization (SEC-002 still works)"
if [ -n "$ALF_DIR" ]; then
  AUTH_TOKEN=$(hostcmd "cat ${ALF_DIR}/secrets/cc_auth_token 2>/dev/null" || echo "")
else
  AUTH_TOKEN=""
fi

if [ -n "$AUTH_TOKEN" ]; then
  ENV_OUT=$(dexec "cat /proc/1/environ | tr '\0' '\n'" 2>/dev/null || echo "")
  assert_not_contains "$ENV_OUT" "VAULT_TOKEN" "No VAULT_TOKEN in daemon process env"
else
  skip "No auth token available — skipping env leak test"
fi
echo ""

# -------------------------------------------------------
echo "[8] Health endpoint"
HEALTH=$(dexec "curl -sf http://localhost:8080/health" 2>/dev/null || echo "FAIL")
if [ "$HEALTH" != "FAIL" ]; then
  pass "Health endpoint responds"
else
  fail "Health endpoint unreachable"
fi
echo ""

# -------------------------------------------------------
echo "[9] DNS resolution"
DNS_TEST=$(dexec "getent hosts api.anthropic.com" 2>/dev/null || echo "")
if [ -n "$DNS_TEST" ]; then
  pass "DNS resolution works (api.anthropic.com)"
else
  fail "DNS resolution failed"
fi
echo ""

# -------------------------------------------------------
echo "[10] resolv.conf accessible"
RESOLV_OWNER=$(dexec "stat -c %u /etc/resolv.conf" 2>/dev/null || echo "unknown")
if [ "$RESOLV_OWNER" = "1000" ]; then
  pass "resolv.conf owned by uid 1000 (applyDNS will work)"
else
  # Not fatal — applyDNS has graceful fallback.
  skip "resolv.conf owned by uid $RESOLV_OWNER (applyDNS will use fallback)"
fi
echo ""

# -------------------------------------------------------
echo "[11] Claude CLI accessible"
CLAUDE_VER=$(dexec_as 1000 "claude --version" 2>/dev/null || echo "FAIL")
if [ "$CLAUDE_VER" != "FAIL" ]; then
  pass "Claude CLI works as uid 1000: $CLAUDE_VER"
else
  fail "Claude CLI not accessible as uid 1000"
fi

CLAUDE_LOCAL=$(dexec "test -L /home/alf/.local/bin/claude && echo ok" 2>/dev/null || echo "FAIL")
if [ "$CLAUDE_LOCAL" = "ok" ]; then
  pass "Claude symlink exists in ~/.local/bin/"
else
  fail "Claude symlink missing from ~/.local/bin/"
fi
echo ""

# -------------------------------------------------------
echo "[12] Log file writable"
LOG_TEST=$(dexec_as 1000 "test -w /home/alf/data/logs/daemon.log && echo ok" 2>/dev/null || echo "FAIL")
assert_equals "$LOG_TEST" "ok" "Daemon log file is writable by uid 1000"
echo ""

# -------------------------------------------------------
echo "[13] No privilege escalation possible"
# NoNewPrivs may not appear in /proc under gVisor. Check docker inspect instead.
NO_NEW_PRIV=$(dexec "awk '/^NoNewPrivs:/{print \$2}' /proc/1/status" 2>/dev/null || echo "")
if [ -n "$NO_NEW_PRIV" ]; then
  assert_equals "$NO_NEW_PRIV" "1" "NoNewPrivs = 1 (no privilege escalation)"
else
  # Fallback: check via docker inspect (works with gVisor).
  DOCKER_NNP=$(hostcmd "docker inspect --format '{{.HostConfig.SecurityOpt}}' $CONTAINER" 2>/dev/null || echo "")
  assert_contains "$DOCKER_NNP" "no-new-privileges" "no-new-privileges set (via docker inspect)"
fi

# Verify uid 1000 cannot su to root.
SETUID_TEST=$(dexec_as 1000 "su -c id root 2>&1 || echo denied" 2>/dev/null || echo "denied")
assert_contains "$SETUID_TEST" "denied\|failure\|cannot\|must be\|Permission" "Cannot su back to root"
echo ""

# -------------------------------------------------------
echo "============================================"
echo -e " Results: ${GREEN}${PASSED} passed${NC}, ${RED}${FAILED} failed${NC}, ${YELLOW}${SKIPPED} skipped${NC}"
echo "============================================"

if [ "$FAILED" -gt 0 ]; then
  echo -e "${RED}SEC-001 verification FAILED${NC}"
  exit 1
else
  echo -e "${GREEN}SEC-001 verification PASSED${NC}"
  exit 0
fi
