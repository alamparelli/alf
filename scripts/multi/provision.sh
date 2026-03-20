#!/usr/bin/env bash
# Provision a new ALF tenant.
# Usage: provision.sh --user <name> --domain <subdomain> [--timezone <tz>] [--image <tag>]
#
# Example:
#   ./provision.sh --user alice --domain alice.alf.example.com --timezone Europe/Rome

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

# ── Parse args ────────────────────────────────────────────────────────
USER="" DOMAIN="" TIMEZONE="UTC" IMAGE="$ALF_IMAGE"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --user)     USER="$2"; shift 2 ;;
        --domain)   DOMAIN="$2"; shift 2 ;;
        --timezone) TIMEZONE="$2"; shift 2 ;;
        --image)    IMAGE="$2"; shift 2 ;;
        *) fatal "Unknown flag: $1" ;;
    esac
done

[[ -z "$USER" ]]   && fatal "Missing --user"
[[ -z "$DOMAIN" ]] && fatal "Missing --domain"

# Validate user name (alphanumeric + hyphens only)
if [[ ! "$USER" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
    fatal "Invalid user name '$USER'. Use lowercase alphanumeric + hyphens."
fi

# ── Provision ─────────────────────────────────────────────────────────
ensure_registry

if tenant_exists "$USER"; then
    fatal "Tenant '$USER' already exists. Use teardown.sh first."
fi

info "Provisioning tenant: ${BOLD}$USER${RESET}"
info "Domain: $DOMAIN"
info "Timezone: $TIMEZONE"
info "Image: $IMAGE"
echo

# Shared infra
mkdir -p "$ALF_MULTI_DIR/letsencrypt"
ensure_shared_whisper_secret
ensure_shared_embed_secret

# Scaffold tenant directories + secrets
scaffold_tenant "$USER"

# Register in tenant registry
add_tenant "$USER" "$DOMAIN" "$TIMEZONE" "$IMAGE"
info "Registered in tenants.json"

# Regenerate compose
generate_compose
echo

# Pull images + start the new tenant (without restarting others)
info "Pulling images..."
docker compose -f "$COMPOSE_FILE" pull "alf-$USER" whisper 2>/dev/null || true

# Pin image to digest for reproducibility (replaces :latest with @sha256:...)
PULLED_DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' "$IMAGE" 2>/dev/null || true)
if [[ -n "$PULLED_DIGEST" ]]; then
    tmp=$(mktemp)
    jq --arg u "$USER" --arg img "$PULLED_DIGEST" \
        'map(if .user == $u then .image = $img else . end)' \
        "$TENANTS_FILE" > "$tmp" && mv "$tmp" "$TENANTS_FILE"
    generate_compose
    info "Pinned image to digest: ${PULLED_DIGEST##*@}"
fi

# Fix any Docker directory placeholders before starting containers
preflight_fix_placeholders

info "Starting alf-$USER..."
docker compose -f "$COMPOSE_FILE" up -d "alf-$USER"

# Also ensure traefik + whisper are running
docker compose -f "$COMPOSE_FILE" up -d traefik whisper

echo
info "Tenant '$USER' is live at https://$DOMAIN"

# Generate magic link
echo
"$SCRIPT_DIR/magic-link.sh" --user "$USER"
