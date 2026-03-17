#!/usr/bin/env bash
# Completely uninstall the multi-tenant ALF infrastructure.
# Stops all containers, removes all data, and optionally reverts userns-remap.
#
# Usage: uninstall.sh [--keep-data] [--revert-userns]
#
#   --keep-data      Preserve tenant data and shared volumes (default: delete everything)
#   --revert-userns  Remove userns-remap from /etc/docker/daemon.json and restart Docker

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

KEEP_DATA=false
REVERT_USERNS=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --keep-data)     KEEP_DATA=true; shift ;;
        --revert-userns) REVERT_USERNS=true; shift ;;
        *) fatal "Unknown flag: $1" ;;
    esac
done

warn "This will completely uninstall the multi-tenant ALF infrastructure."
if [[ "$KEEP_DATA" == false ]]; then
    warn "ALL tenant data, secrets, and configuration will be DELETED."
fi
echo
read -rp "Type 'yes' to confirm: " confirm
[[ "$confirm" != "yes" ]] && fatal "Aborted."
echo

# ── Stop and remove all containers ───────────────────────────────────
if [[ -f "$COMPOSE_FILE" ]]; then
    info "Stopping all containers..."
    docker compose -f "$COMPOSE_FILE" down --remove-orphans 2>/dev/null || true
else
    info "No compose file found — stopping containers by name..."
    # Fallback: stop any lingering alf-* containers
    docker ps -a --format '{{.Names}}' | grep -E '^alf-' | xargs -r docker rm -f 2>/dev/null || true
fi

# ── Remove Docker networks ────────────────────────────────────────────
info "Removing Docker networks..."
docker network rm alf-multi_whisper-internal 2>/dev/null || true
docker network rm alf-multi_default 2>/dev/null || true

# ── Remove data ───────────────────────────────────────────────────────
if [[ "$KEEP_DATA" == false ]]; then
    info "Removing $ALF_MULTI_DIR..."
    rm -rf "${ALF_MULTI_DIR:?}"
    info "Removed."
else
    warn "Data preserved at: $ALF_MULTI_DIR"
    info "Removing compose file and scripts only..."
    rm -f "$COMPOSE_FILE"
fi

# ── Remove installed scripts ──────────────────────────────────────────
SCRIPTS_DIR="${ALF_MULTI_DIR}/bin"
if [[ -d "$SCRIPTS_DIR" ]] && [[ "$KEEP_DATA" == false ]]; then
    : # already removed with ALF_MULTI_DIR above
elif [[ -d "$SCRIPTS_DIR" ]]; then
    info "Removing installed scripts at $SCRIPTS_DIR..."
    rm -rf "$SCRIPTS_DIR"
fi

# ── Revert userns-remap (optional) ───────────────────────────────────
if [[ "$REVERT_USERNS" == true ]]; then
    local cfg=/etc/docker/daemon.json
    if [[ -f "$cfg" ]] && grep -q "userns-remap" "$cfg"; then
        info "Reverting Docker userns-remap..."
        local tmp; tmp=$(mktemp)
        jq 'del(."userns-remap")' "$cfg" > "$tmp" && mv "$tmp" "$cfg"
        systemctl restart docker
        sleep 3
        info "Docker restarted without userns-remap"
    else
        info "userns-remap not configured — skipping"
    fi
fi

echo
info "Multi-tenant ALF uninstalled."
if [[ "$KEEP_DATA" == true ]]; then
    echo "  Data preserved at: $ALF_MULTI_DIR"
fi
echo
