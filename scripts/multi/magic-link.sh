#!/usr/bin/env bash
# Generate a magic link for a specific tenant.
# Usage: magic-link.sh --user <name>

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

USER=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --user) USER="$2"; shift 2 ;;
        *) fatal "Unknown flag: $1" ;;
    esac
done

[[ -z "$USER" ]] && fatal "Missing --user"

ensure_registry

if ! tenant_exists "$USER"; then
    fatal "Tenant '$USER' not found."
fi

domain=$(get_tenant_field "$USER" "domain")

# Read the tenant's auth token
token_file="$TENANTS_DIR/$USER/secrets/cc_auth_token"
if [[ ! -s "$token_file" ]]; then
    fatal "No cc_auth_token found for $USER"
fi
token=$(cat "$token_file")

# Wait for daemon to be ready (up to 30s)
for i in $(seq 1 15); do
    link=$(docker exec "alf-$USER" \
        curl -sf -X POST http://localhost:8080/api/magic-link \
        -H "Authorization: Bearer $token" 2>/dev/null) && break
    sleep 2
done

if [[ -z "$link" ]]; then
    fatal "Failed to generate magic link. Is alf-$USER running?"
fi

echo
info "Magic link for ${BOLD}$USER${RESET}:"
echo -e "  ${CYAN}${link}${RESET}"
echo
