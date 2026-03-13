#!/usr/bin/env bash
set -euo pipefail

# Deploy ALF alpha distribution server + upload binaries.
# Usage: ./scripts/alpha-deploy.sh [--password <pw>] [--binaries-only]
#
# First run:  ./scripts/alpha-deploy.sh --password mysecret
# Updates:    ./scripts/alpha-deploy.sh --binaries-only

REMOTE_HOST="alessandro@192.168.129.101"
REMOTE_DIR="/home/alessandro/alf-alpha"
SCRIPT_DIR="$(cd "$(dirname "$0")/alpha-server" && pwd)"

PASSWORD=""
BINARIES_ONLY=false

while [ $# -gt 0 ]; do
  case "$1" in
    --password) PASSWORD="$2"; shift 2 ;;
    --binaries-only) BINARIES_ONLY=true; shift ;;
    *) echo "Unknown flag: $1"; exit 1 ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"

# SSH multiplexing
SSH_SOCK="/tmp/alf-alpha-ssh-$$"
ssh -fNM -S "$SSH_SOCK" "$REMOTE_HOST"
cleanup() { ssh -S "$SSH_SOCK" -O exit "$REMOTE_HOST" 2>/dev/null || true; }
trap cleanup EXIT

SSH="ssh -S $SSH_SOCK"
SCP="scp -o ControlPath=$SSH_SOCK"
RSYNC="rsync -az -e 'ssh -S $SSH_SOCK'"

GIT_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
GIT_HASH=$(git rev-parse --short HEAD)
if [ -n "$GIT_TAG" ]; then
  VERSION="${GIT_TAG#v}"
else
  VERSION="alpha-${GIT_HASH}"
fi

# --- Build CLI binaries for all platforms ---
echo "==> Building CLI binaries (${VERSION})..."
LDFLAGS="-s -w -X main.version=${VERSION}"
TMPBIN=$(mktemp -d)

# Vendor vault-proxy
rm -rf third_party/vault-proxy
mkdir -p third_party/vault-proxy
rsync -a --exclude .git --exclude vault-data --exclude '/vault-server' --exclude '/vault-cli' \
  ../Projects/vault-proxy/ third_party/vault-proxy/

CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "${TMPBIN}/alf-linux-amd64"  ./cmd/alf
CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -ldflags="$LDFLAGS" -o "${TMPBIN}/alf-linux-arm64"  ./cmd/alf
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "${TMPBIN}/alf-darwin-amd64" ./cmd/alf
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$LDFLAGS" -o "${TMPBIN}/alf-darwin-arm64" ./cmd/alf

rm -rf third_party/vault-proxy

echo "==> Binaries built:"
ls -lh "${TMPBIN}/"

# --- Bootstrap remote directory ---
$SSH "$REMOTE_HOST" "mkdir -p ${REMOTE_DIR}/dist"

# --- Upload binaries ---
echo "==> Uploading binaries to ${REMOTE_HOST}:${REMOTE_DIR}/dist/..."
$SCP "${TMPBIN}"/alf-* "$REMOTE_HOST:${REMOTE_DIR}/dist/"
rm -rf "$TMPBIN"

# --- Upload install script ---
echo "==> Uploading install script..."
$SCP "${SCRIPT_DIR}/install.sh" "$REMOTE_HOST:${REMOTE_DIR}/dist/"

if [ "$BINARIES_ONLY" = true ]; then
  echo "==> Done (binaries only)."
  exit 0
fi

# --- Setup server ---
echo "==> Setting up alpha server..."

# Generate htpasswd
if [ -z "$PASSWORD" ]; then
  # Check if htpasswd already exists on remote
  if $SSH "$REMOTE_HOST" "[ -f ${REMOTE_DIR}/htpasswd ]"; then
    echo "  Using existing htpasswd."
  else
    echo "Error: --password required for first-time setup."
    echo "Usage: ./scripts/alpha-deploy.sh --password <pw>"
    exit 1
  fi
else
  echo "  Generating htpasswd..."
  # Use openssl for portable htpasswd generation (no need for apache2-utils)
  HASH=$(openssl passwd -apr1 "$PASSWORD")
  echo "alpha:${HASH}" > /tmp/alf-alpha-htpasswd
  $SCP /tmp/alf-alpha-htpasswd "$REMOTE_HOST:${REMOTE_DIR}/htpasswd"
  rm -f /tmp/alf-alpha-htpasswd
fi

# Upload nginx config
echo "==> Uploading nginx config..."
$SCP "${SCRIPT_DIR}/nginx.conf" "$REMOTE_HOST:${REMOTE_DIR}/nginx.conf"

# Upload docker-compose
echo "==> Uploading docker-compose..."
$SCP "${SCRIPT_DIR}/docker-compose.yml" "$REMOTE_HOST:${REMOTE_DIR}/docker-compose.yml"

# Start/restart
echo "==> Starting alpha server..."
$SSH "$REMOTE_HOST" "cd ${REMOTE_DIR} && docker compose up -d"

echo ""
echo "==> Alpha server deployed!"
echo ""
echo "Install command for alpha users:"
echo ""
echo "  curl -fsSL https://cc.lamparelli.eu/alpha/install.sh | ALF_TOKEN=${PASSWORD:-<password>} sh"
echo ""
