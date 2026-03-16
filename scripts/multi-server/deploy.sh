#!/usr/bin/env bash
# Deploy the multi-tenant installer to the alpha server on homelab.
# Usage: ./deploy.sh [--password <htpasswd_password>]
#
# This builds the self-extracting bundle and uploads it alongside
# the alpha server config. Requires the alpha-server to be running.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALPHA_DIR="$SCRIPT_DIR/../alpha-server"
REMOTE_HOST="alessandro@192.168.129.101"
REMOTE_ALPHA_DIR="/home/alessandro/alf-alpha"

PASSWORD=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --password) PASSWORD="$2"; shift 2 ;;
        *) echo "Unknown flag: $1"; exit 1 ;;
    esac
done

# SSH multiplexing
SSH_SOCK="/tmp/alf-multi-deploy-$$"
ssh -fNM -S "$SSH_SOCK" "$REMOTE_HOST"
cleanup() { ssh -S "$SSH_SOCK" -O exit "$REMOTE_HOST" 2>/dev/null || true; }
trap cleanup EXIT

SSH="ssh -S $SSH_SOCK"
SCP="scp -o ControlPath=$SSH_SOCK"

echo "==> Building self-extracting installer..."
"$SCRIPT_DIR/bundle.sh"

echo "==> Uploading multi-server dist..."
$SSH "$REMOTE_HOST" "mkdir -p $REMOTE_ALPHA_DIR/multi-dist"
rsync -az -e "ssh -S $SSH_SOCK" \
    "$SCRIPT_DIR/dist/" "$REMOTE_HOST:$REMOTE_ALPHA_DIR/multi-dist/"

echo "==> Uploading updated alpha-server config..."
$SCP "$ALPHA_DIR/nginx.conf" "$REMOTE_HOST:$REMOTE_ALPHA_DIR/nginx.conf"
$SCP "$ALPHA_DIR/docker-compose.yml" "$REMOTE_HOST:$REMOTE_ALPHA_DIR/docker-compose.yml"

# Generate htpasswd for /s/ endpoint if password provided
if [ -n "$PASSWORD" ]; then
    echo "==> Generating htpasswd-s..."
    HTPASSWD=$(docker run --rm httpd:alpine htpasswd -nb s "$PASSWORD" 2>/dev/null || htpasswd -nb s "$PASSWORD")
    echo "$HTPASSWD" | $SSH "$REMOTE_HOST" "cat > $REMOTE_ALPHA_DIR/htpasswd-s"
    $SSH "$REMOTE_HOST" "chmod 600 $REMOTE_ALPHA_DIR/htpasswd-s"
elif ! $SSH "$REMOTE_HOST" "test -f $REMOTE_ALPHA_DIR/htpasswd-s"; then
    echo "WARNING: No htpasswd-s found on remote. Use --password to set one."
fi

# Fix docker-compose volume path for multi-server dist (local dev path vs remote path)
$SSH "$REMOTE_HOST" "cd $REMOTE_ALPHA_DIR && \
    sed -i 's|../multi-server/dist:/usr/share/nginx/html/s:ro|./multi-dist:/usr/share/nginx/html/s:ro|' docker-compose.yml"

echo "==> Restarting alpha server..."
$SSH "$REMOTE_HOST" "cd $REMOTE_ALPHA_DIR && docker compose up -d --force-recreate"

echo ""
echo "Done. Multi-tenant installer available at:"
echo "  curl -fsSL https://cc.lamparelli.eu/s/setup.sh | S_TOKEN=<password> sh"
echo ""
