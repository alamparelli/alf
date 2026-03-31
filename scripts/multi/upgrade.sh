#!/usr/bin/env bash
# Upgrade all ALF multi-tenant services.
#
# What it does:
#   1. Regenerate docker-compose.yml (picks up template changes)
#   2. Pull latest images (alf + whisper)
#   3. Per-tenant: fix permissions, seed skills & agents, harden secrets
#   4. Restart all services
#
# Usage: upgrade.sh [--no-restart] [--skip-pull]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

# Load saved config (ACME_EMAIL, ALF_IMAGE, etc.) from init.sh
[[ -f "$ALF_MULTI_DIR/.env" ]] && source "$ALF_MULTI_DIR/.env"

NO_RESTART=false
SKIP_PULL=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-restart) NO_RESTART=true; shift ;;
        --skip-pull)  SKIP_PULL=true; shift ;;
        *)            fatal "Unknown flag: $1" ;;
    esac
done

ensure_registry

TENANT_COUNT=$(jq length "$TENANTS_FILE")
if [[ "$TENANT_COUNT" -eq 0 ]]; then
    fatal "No tenants registered. Nothing to upgrade."
fi

info "Upgrading $TENANT_COUNT tenant(s)..."
echo

# ── Step 0: Unpin digest-locked images back to :latest ──────────────
# provision.sh pins images to sha256 digests for reproducibility.
# Upgrade needs to move all tenants back to the floating tag so they
# pick up the newly pulled image.
UNPINNED=0
tmp=$(mktemp)
jq --arg img "$ALF_IMAGE" '
    [.[] | if (.image | test("@sha256:")) then .image = $img else . end]
' "$TENANTS_FILE" > "$tmp" && mv "$tmp" "$TENANTS_FILE"
UNPINNED=$(jq '[.[] | select(.image == "'"$ALF_IMAGE"'")] | length' "$TENANTS_FILE")
info "Reset $UNPINNED tenant(s) to $ALF_IMAGE"
echo

# ── Step 1: Regenerate compose ──────────────────────────────────────
info "Regenerating docker-compose.yml..."
generate_compose
preflight_fix_placeholders
echo

# ── Step 2: Pull latest images ──────────────────────────────────────
if [[ "$SKIP_PULL" == false ]]; then
    info "Pulling latest images..."
    docker compose -f "$COMPOSE_FILE" pull --ignore-buildable 2>&1 || {
        warn "Some images failed to pull (may not exist in registry yet). Continuing..."
    }
    echo
else
    warn "Skipping image pull (--skip-pull)"
    echo
fi

# ── Step 3: Per-tenant maintenance ──────────────────────────────────
# Skills/agents source: dev repo layout OR bundled defaults
SKILLS_SRC="$SCRIPT_DIR/../../skills.d"
[[ ! -d "$SKILLS_SRC" ]] && SKILLS_SRC="$ALF_MULTI_DIR/defaults/skills.d"
AGENTS_SRC="$SCRIPT_DIR/../../internal/cli/bundled_agents"
[[ ! -d "$AGENTS_SRC" ]] && AGENTS_SRC="$ALF_MULTI_DIR/defaults/agents"

while IFS= read -r tenant; do
    [[ -z "$tenant" ]] && continue

    local_user=$(echo "$tenant" | jq -r '.user')
    tenant_dir="$TENANTS_DIR/$local_user"

    info "[$local_user] Running maintenance..."

    # 3a. Fix permissions (data, config, skills, cache, vault — uid 1000)
    host_uid=$(host_uid_for 1000)
    for subdir in data config.d skills.d cache local vault-data; do
        if [[ -d "$tenant_dir/$subdir" ]]; then
            chown -R "$host_uid:$host_uid" "$tenant_dir/$subdir" 2>/dev/null || true
        fi
    done
    echo "  permissions fixed"

    # 3b. Harden secrets (0644 so container uid 1000 can read bind-mounts)
    if [[ -d "$tenant_dir/secrets" ]]; then
        chmod 755 "$tenant_dir/secrets"
        for secret_file in "$tenant_dir/secrets"/*; do
            [[ -f "$secret_file" ]] && chmod 644 "$secret_file"
        done
        echo "  secrets hardened"
    fi

    # 3c. Seed bundled skills (always overwrite with latest bundled version)
    if [[ -d "$SKILLS_SRC" ]]; then
        mkdir -p "$tenant_dir/skills.d"
        for skill_dir in "$SKILLS_SRC"/*/; do
            [[ -d "$skill_dir" ]] || continue
            skill_name=$(basename "$skill_dir")
            dest="$tenant_dir/skills.d/$skill_name"
            mkdir -p "$dest"
            cp -r "$skill_dir"* "$dest/" 2>/dev/null || true
            chown -R "$host_uid:$host_uid" "$dest" 2>/dev/null || true
        done
        echo "  skills synced"
    fi

    # 3d. Seed bundled agents (don't overwrite existing)
    if [[ -d "$AGENTS_SRC" ]]; then
        mkdir -p "$tenant_dir/data/agents/teams"
        for agent_file in "$AGENTS_SRC"/*.json; do
            [[ ! -f "$agent_file" ]] && continue
            agent_name=$(basename "$agent_file")
            if [[ ! -f "$tenant_dir/data/agents/teams/$agent_name" ]]; then
                cp "$agent_file" "$tenant_dir/data/agents/teams/$agent_name"
                chown "$host_uid:$host_uid" "$tenant_dir/data/agents/teams/$agent_name" 2>/dev/null || true
                echo "  seeded agent: $agent_name"
            fi
        done
    fi

    # 3e. Ensure shared secrets are propagated
    for shared_secret in whisper_shared_secret embed_shared_secret; do
        if [[ -f "$SHARED_DIR/$shared_secret" ]]; then
            cp "$SHARED_DIR/$shared_secret" "$tenant_dir/secrets/$shared_secret"
            chmod 644 "$tenant_dir/secrets/$shared_secret"
        fi
    done

done <<< "$(jq -c '.[]' "$TENANTS_FILE")"

echo

# ── Step 4: Restart ─────────────────────────────────────────────────
if [[ "$NO_RESTART" == true ]]; then
    warn "Skipping restart (--no-restart). Run 'apply.sh' when ready."
else
    info "Restarting all services..."
    docker compose -f "$COMPOSE_FILE" up -d
    echo
    info "Running containers:"
    docker compose -f "$COMPOSE_FILE" ps --format "table {{.Name}}\t{{.Status}}\t{{.Image}}"
fi

echo
info "Upgrade complete."
