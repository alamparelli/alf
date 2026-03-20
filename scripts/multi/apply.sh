#!/usr/bin/env bash
# Apply (or re-apply) the compose configuration.
# Use after: script updates, config changes, or reinstall after --keep-data uninstall.
#
# Usage: apply.sh [--pull]
#
#   --pull   Pull latest images before starting

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

PULL=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --pull) PULL=true; shift ;;
        *) fatal "Unknown flag: $1" ;;
    esac
done

ensure_registry
ensure_shared_whisper_secret
ensure_shared_embed_secret
generate_compose
preflight_fix_placeholders

if [[ "$PULL" == true ]]; then
    info "Pulling latest images..."
    docker compose -f "$COMPOSE_FILE" pull
fi

info "Starting all services..."
docker compose -f "$COMPOSE_FILE" up -d

echo
info "Done. Running containers:"
docker compose -f "$COMPOSE_FILE" ps --format "table {{.Name}}\t{{.Status}}"
echo
