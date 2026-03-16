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

# Call the daemon's magic-link endpoint inside the container
link=$(docker exec "alf-$USER" \
    curl -sf -X POST http://localhost:8080/api/magic-link 2>/dev/null) || {
    fatal "Failed to generate magic link. Is alf-$USER running?"
}

echo
info "Magic link for ${BOLD}$USER${RESET}:"
echo -e "  ${CYAN}${link}${RESET}"
echo
