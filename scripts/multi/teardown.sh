#!/usr/bin/env bash
# Remove an ALF tenant.
# Usage: teardown.sh --user <name> [--keep-data]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

USER="" KEEP_DATA=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --user)      USER="$2"; shift 2 ;;
        --keep-data) KEEP_DATA=true; shift ;;
        *) fatal "Unknown flag: $1" ;;
    esac
done

[[ -z "$USER" ]] && fatal "Missing --user"

ensure_registry

if ! tenant_exists "$USER"; then
    fatal "Tenant '$USER' not found."
fi

domain=$(get_tenant_field "$USER" "domain")
info "Tearing down tenant: ${BOLD}$USER${RESET} ($domain)"
echo

# Stop and remove the container
info "Stopping alf-$USER..."
docker compose -f "$COMPOSE_FILE" stop "alf-$USER" 2>/dev/null || true
docker compose -f "$COMPOSE_FILE" rm -f "alf-$USER" 2>/dev/null || true

# Remove from registry
remove_tenant "$USER"
info "Removed from tenants.json"

# Regenerate compose (without this tenant)
generate_compose

if [[ "$KEEP_DATA" == true ]]; then
    warn "Tenant data preserved at: $TENANTS_DIR/$USER"
else
    info "Removing tenant data..."
    rm -rf "${TENANTS_DIR:?}/$USER"
    info "Data removed."
fi

echo
info "Tenant '$USER' has been torn down."
