#!/usr/bin/env bash
# Initialize the multi-tenant ALF infrastructure.
# Run this once on a fresh VPS before provisioning tenants.
#
# Required env vars:
#   ACME_EMAIL    - Let's Encrypt email for TLS certificates
#
# Optional env vars:
#   ALF_MULTI_DIR - Base directory (default: /opt/alf-multi)
#   ALF_IMAGE     - Default ALF Docker image
#   WHISPER_MODEL - Whisper model size (default: small)
#
# Example:
#   ACME_EMAIL=you@example.com ./init.sh

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

[[ -z "$ACME_EMAIL" ]] && fatal "ACME_EMAIL is required. Export it before running."

info "Initializing multi-tenant ALF at: ${BOLD}$ALF_MULTI_DIR${RESET}"
echo

# ── Configure Docker userns-remap ────────────────────────────────────
# Maps container root (uid 0) → unprivileged host uid, reducing container
# escape blast radius. Must be done before any containers are started.
configure_userns_remap() {
    local cfg=/etc/docker/daemon.json

    # Already active?
    if docker info 2>/dev/null | grep -q "userns"; then
        info "userns-remap already active — skipping"
        return 0
    fi

    info "Configuring Docker userns-remap (container root isolation)..."

    # Ensure dockremap user exists with subuid/subgid
    if ! id dockremap &>/dev/null; then
        useradd -r -s /bin/false dockremap 2>/dev/null || true
    fi
    if ! grep -q "^dockremap:" /etc/subuid 2>/dev/null; then
        echo "dockremap:100000:65536" >> /etc/subuid
    fi
    if ! grep -q "^dockremap:" /etc/subgid 2>/dev/null; then
        echo "dockremap:100000:65536" >> /etc/subgid
    fi

    # Merge into daemon.json
    if [[ -f "$cfg" ]]; then
        local tmp; tmp=$(mktemp)
        jq '. + {"userns-remap": "default"}' "$cfg" > "$tmp" && mv "$tmp" "$cfg"
    else
        echo '{"userns-remap": "default"}' > "$cfg"
    fi

    systemctl restart docker
    sleep 3
    info "Docker restarted with userns-remap"
}

configure_userns_remap

# Create directory structure
ensure_registry
mkdir -p "$ALF_MULTI_DIR/letsencrypt"
mkdir -p "$SHARED_DIR/models"
chown -R 1000:1000 "$SHARED_DIR/models"

# Generate shared whisper secret
ensure_shared_whisper_secret

# Write env file for persistence
cat > "$ALF_MULTI_DIR/.env" <<EOF
ALF_RUNTIME=runc
ACME_EMAIL=${ACME_EMAIL}
ALF_IMAGE=${ALF_IMAGE}
WHISPER_IMAGE=${WHISPER_IMAGE}
WHISPER_MODEL=${WHISPER_MODEL}
EOF
info "Saved .env"

# Generate initial compose (infra only, no tenants yet)
generate_compose

echo
info "Infrastructure initialized."
echo
echo "  Directory:  $ALF_MULTI_DIR"
echo "  Compose:    $COMPOSE_FILE"
echo "  Tenants:    $TENANTS_FILE"
echo
echo "  Next steps:"
echo "    1. Configure DNS: *.alf.yourdomain.com → this VPS IP"
echo "    2. Provision a tenant:"
echo "       ./provision.sh --user alice --domain alice.alf.example.com"
echo
