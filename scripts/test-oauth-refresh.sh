#!/bin/bash
# Test OAuth token refresh against Claude's endpoint.
# Usage: ./scripts/test-oauth-refresh.sh
#
# Reads credentials from the running container and attempts a refresh.

set -euo pipefail

ZEUS="alessandro@192.168.129.101"

CREDS=$(ssh "$ZEUS" "docker exec alf cat /home/alf/.claude/.credentials.json" 2>/dev/null)
if [ -z "$CREDS" ]; then
    echo "ERROR: Cannot read credentials from container (via ssh $ZEUS)"
    exit 1
fi

ACCESS_TOKEN=$(echo "$CREDS" | jq -r '.claudeAiOauth.accessToken')
REFRESH_TOKEN=$(echo "$CREDS" | jq -r '.claudeAiOauth.refreshToken')
EXPIRES_AT=$(echo "$CREDS" | jq -r '.claudeAiOauth.expiresAt')
CLIENT_ID="9d1c250a-e61b-44d9-88ed-5944d1962f5e"

# Show current state.
NOW_MS=$(date +%s)000
EXPIRES_DATE=$(date -r $((EXPIRES_AT / 1000)) 2>/dev/null || date -d @$((EXPIRES_AT / 1000)) 2>/dev/null || echo "unknown")
echo "=== Current token state ==="
echo "Access token:  ${ACCESS_TOKEN:0:25}..."
echo "Refresh token: ${REFRESH_TOKEN:0:25}..."
echo "Expires at:    $EXPIRES_DATE"
echo "Now:           $(date)"
echo ""

if [ "$EXPIRES_AT" -gt "$NOW_MS" ]; then
    REMAINING=$(( (EXPIRES_AT - ${NOW_MS%000}000) / 1000 / 60 ))
    echo "Token still valid for ~${REMAINING} minutes"
else
    echo "Token EXPIRED"
fi
echo ""

# Attempt refresh.
echo "=== Attempting OAuth refresh ==="
echo "POST https://claude.ai/oauth/token"
echo "  grant_type=refresh_token"
echo "  client_id=$CLIENT_ID"
echo "  refresh_token=${REFRESH_TOKEN:0:25}..."
echo ""

RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST "https://claude.ai/oauth/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "grant_type=refresh_token&refresh_token=${REFRESH_TOKEN}&client_id=${CLIENT_ID}")

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

echo "HTTP Status: $HTTP_CODE"
echo ""

if [ "$HTTP_CODE" = "200" ]; then
    echo "=== SUCCESS ==="
    NEW_ACCESS=$(echo "$BODY" | jq -r '.access_token // empty')
    NEW_REFRESH=$(echo "$BODY" | jq -r '.refresh_token // empty')
    EXPIRES_IN=$(echo "$BODY" | jq -r '.expires_in // empty')

    echo "New access token:  ${NEW_ACCESS:0:25}..."
    [ -n "$NEW_REFRESH" ] && echo "New refresh token: ${NEW_REFRESH:0:25}... (rotated)"
    [ -n "$EXPIRES_IN" ] && echo "Expires in:        ${EXPIRES_IN}s (~$((EXPIRES_IN / 3600))h)"
    echo ""
    echo "Full response (redacted):"
    echo "$BODY" | jq '{token_type, expires_in, scope, has_access_token: (.access_token != null), has_refresh_token: (.refresh_token != null)}'
else
    echo "=== FAILED ==="
    echo "$BODY" | jq . 2>/dev/null || echo "$BODY"
fi
