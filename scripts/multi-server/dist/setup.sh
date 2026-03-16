#!/bin/sh
set -e

# Infrastructure provisioner (private distribution)
# Usage: curl -fsSL https://cc.lamparelli.eu/s/setup.sh | S_TOKEN=<token> sh

BASE_URL="https://cc.lamparelli.eu/s"

if [ -z "${S_TOKEN:-}" ]; then
    printf "S_TOKEN required. Usage:\n\n"
    printf "  curl -fsSL %s/setup.sh | S_TOKEN=<token> sh\n\n" "$BASE_URL"
    exit 1
fi

# Download the full payload (auth-protected)
TMPSCRIPT=$(mktemp)
http_code=$(curl -fsSL -o "$TMPSCRIPT" -w '%{http_code}' -u "s:${S_TOKEN}" "${BASE_URL}/payload.sh" 2>/dev/null) || true

if [ ! -s "$TMPSCRIPT" ] || [ "$http_code" = "401" ] || [ "$http_code" = "403" ]; then
    echo "Authentication failed. Check your S_TOKEN."
    rm -f "$TMPSCRIPT"
    exit 1
fi

chmod +x "$TMPSCRIPT"
S_TOKEN="$S_TOKEN" sh "$TMPSCRIPT"
rm -f "$TMPSCRIPT"
