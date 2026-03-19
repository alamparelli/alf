#!/usr/bin/env bash
# Shared functions for multi-tenant ALF management.
# Source this file, don't execute it.

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────
ALF_MULTI_DIR="${ALF_MULTI_DIR:-/opt/alf-multi}"
ALF_IMAGE="${ALF_IMAGE:-ghcr.io/alamparelli/alf:latest}"
WHISPER_IMAGE="${WHISPER_IMAGE:-ghcr.io/alamparelli/whisper-service:latest}"
WHISPER_MODEL="${WHISPER_MODEL:-small}"
ACME_EMAIL="${ACME_EMAIL:-a.lamparelli@gmail.com}"

TENANTS_FILE="$ALF_MULTI_DIR/tenants.json"
TENANTS_DIR="$ALF_MULTI_DIR/tenants"
SHARED_DIR="$ALF_MULTI_DIR/shared"
COMPOSE_FILE="$ALF_MULTI_DIR/docker-compose.yml"

# ── Colors ────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'
CYAN='\033[0;36m'; DIM='\033[2m'; BOLD='\033[1m'; RESET='\033[0m'

info()  { echo -e "${GREEN}[+]${RESET} $*"; }
warn()  { echo -e "${YELLOW}[!]${RESET} $*"; }
err()   { echo -e "${RED}[x]${RESET} $*" >&2; }
fatal() { err "$@"; exit 1; }

# ── Tenant registry ──────────────────────────────────────────────────
ensure_registry() {
    mkdir -p "$ALF_MULTI_DIR" "$TENANTS_DIR" "$SHARED_DIR"
    if [[ ! -f "$TENANTS_FILE" ]]; then
        echo '[]' > "$TENANTS_FILE"
    fi
}

tenant_exists() {
    local user="$1"
    jq -e --arg u "$user" '.[] | select(.user == $u)' "$TENANTS_FILE" >/dev/null 2>&1
}

add_tenant() {
    local user="$1" domain="$2" timezone="${3:-UTC}" image="${4:-$ALF_IMAGE}"
    local tmp
    tmp=$(mktemp)
    jq --arg u "$user" --arg d "$domain" --arg tz "$timezone" --arg img "$image" \
        '. + [{"user": $u, "domain": $d, "timezone": $tz, "image": $img, "created": (now | todate)}]' \
        "$TENANTS_FILE" > "$tmp" && mv "$tmp" "$TENANTS_FILE"
}

remove_tenant() {
    local user="$1"
    local tmp
    tmp=$(mktemp)
    jq --arg u "$user" '[.[] | select(.user != $u)]' "$TENANTS_FILE" > "$tmp" && mv "$tmp" "$TENANTS_FILE"
}

get_tenant_field() {
    local user="$1" field="$2"
    jq -r --arg u "$user" --arg f "$field" '.[] | select(.user == $u) | .[$f]' "$TENANTS_FILE"
}

list_tenants() {
    jq -r '.[] | [.user, .domain, .timezone, .image, .created] | @tsv' "$TENANTS_FILE"
}

# ── Tenant directory scaffolding ─────────────────────────────────────
scaffold_tenant() {
    local user="$1"
    local dir="$TENANTS_DIR/$user"

    mkdir -p "$dir"/{data/{logs,sessions,tools,skills,agents/teams},config.d,skills.d,local}
    mkdir -p "$dir"/cache/{claude,local,npm,cache}
    mkdir -p "$dir"/vault-data
    mkdir -p "$dir"/secrets

    # resolv.conf (remove directory placeholder Docker may have created)
    [[ -d "$dir/resolv.conf" ]] && rm -rf "$dir/resolv.conf"
    if [[ ! -f "$dir/resolv.conf" ]]; then
        echo "nameserver 8.8.8.8" > "$dir/resolv.conf"
        echo "nameserver 1.1.1.1" >> "$dir/resolv.conf"
    fi

    # Ensure all secret files exist (empty placeholders for Docker Compose)
    local secrets=(telegram_bot_token telegram_chat_id cc_auth_token openrouter_api_key
                   openai_api_key claude_oauth_token vault_master_password whisper_shared_secret)
    for s in "${secrets[@]}"; do
        # Remove directory placeholder Docker may have created
        [[ -d "$dir/secrets/$s" ]] && rm -rf "$dir/secrets/$s"
        if [[ ! -f "$dir/secrets/$s" ]]; then
            touch "$dir/secrets/$s"
            chmod 644 "$dir/secrets/$s"
        fi
    done

    # Auto-generate cc_auth_token if empty
    if [[ ! -s "$dir/secrets/cc_auth_token" ]]; then
        openssl rand -hex 32 > "$dir/secrets/cc_auth_token"
        chmod 644 "$dir/secrets/cc_auth_token"
        info "Generated cc_auth_token for $user"
    fi

    # Copy shared whisper secret
    if [[ -f "$SHARED_DIR/whisper_shared_secret" ]]; then
        cp "$SHARED_DIR/whisper_shared_secret" "$dir/secrets/whisper_shared_secret"
        chmod 644 "$dir/secrets/whisper_shared_secret"
    fi

    # Fix ownership — use remapped host UID if userns-remap is active
    local host_uid
    host_uid=$(host_uid_for 1000)
    chown -R "$host_uid:$host_uid" "$dir/data" "$dir/cache" "$dir/vault-data" "$dir/config.d" "$dir/skills.d" "$dir/local" 2>/dev/null || true
}

# ── userns-remap UID resolution ──────────────────────────────────────
# Returns the host UID that corresponds to a given container UID,
# accounting for Docker userns-remap (if active).
host_uid_for() {
    local container_uid="${1:-1000}"
    if docker info 2>/dev/null | grep -q "userns"; then
        local offset
        offset=$(awk -F: '/^dockremap/{print $2}' /etc/subuid 2>/dev/null || echo 0)
        echo $((offset + container_uid))
    else
        echo "$container_uid"
    fi
}

# ── Pre-flight: fix Docker directory placeholders ────────────────────
# Docker creates directories when mounting files that don't exist yet.
# This must run BEFORE every `docker compose up`.
preflight_fix_placeholders() {
    # Shared files
    local shared_files=("$SHARED_DIR/whisper_shared_secret")
    for f in "${shared_files[@]}"; do
        if [[ -d "$f" ]]; then
            rm -rf "$f"
            info "Removed directory placeholder: $f"
        fi
    done

    # Per-tenant files
    for tenant_dir in "$TENANTS_DIR"/*/; do
        [[ ! -d "$tenant_dir" ]] && continue
        # resolv.conf
        [[ -d "${tenant_dir}resolv.conf" ]] && rm -rf "${tenant_dir}resolv.conf"
        # secrets
        if [[ -d "${tenant_dir}secrets" ]]; then
            for s in "${tenant_dir}secrets"/*; do
                [[ -d "$s" ]] && rm -rf "$s"
            done
        fi
    done

    # Recreate all required files
    ensure_shared_whisper_secret

    for tenant_dir in "$TENANTS_DIR"/*/; do
        [[ ! -d "$tenant_dir" ]] && continue
        local user
        user=$(basename "$tenant_dir")
        # resolv.conf
        if [[ ! -f "${tenant_dir}resolv.conf" ]]; then
            echo "nameserver 8.8.8.8" > "${tenant_dir}resolv.conf"
            echo "nameserver 1.1.1.1" >> "${tenant_dir}resolv.conf"
        fi
        # secrets
        local secrets=(telegram_bot_token telegram_chat_id cc_auth_token openrouter_api_key
                       openai_api_key claude_oauth_token vault_master_password whisper_shared_secret)
        for s in "${secrets[@]}"; do
            if [[ ! -f "${tenant_dir}secrets/$s" ]]; then
                touch "${tenant_dir}secrets/$s"
                chmod 644 "${tenant_dir}secrets/$s"
            fi
        done
        # Copy whisper secret if missing
        if [[ ! -s "${tenant_dir}secrets/whisper_shared_secret" ]] && [[ -f "$SHARED_DIR/whisper_shared_secret" ]]; then
            cp "$SHARED_DIR/whisper_shared_secret" "${tenant_dir}secrets/whisper_shared_secret"
            chmod 644 "${tenant_dir}secrets/whisper_shared_secret"
        fi
    done
}

# ── Shared infrastructure ────────────────────────────────────────────
ensure_shared_whisper_secret() {
    local path="$SHARED_DIR/whisper_shared_secret"
    # Remove directory placeholder Docker may have created
    [[ -d "$path" ]] && rm -rf "$path"
    if [[ ! -s "$path" ]]; then
        mkdir -p "$SHARED_DIR"
        openssl rand -hex 32 > "$path"
        chmod 644 "$path"
        info "Generated shared whisper secret"
    fi
    # Ensure models dir exists — whisper runs as uid 1000 (user 'whisper') with userns_mode: host
    mkdir -p "$SHARED_DIR/models"
    chown -R 1000:1000 "$SHARED_DIR/models"
}

# ── Compose generation ───────────────────────────────────────────────
generate_compose() {
    if [[ -z "$ACME_EMAIL" ]]; then
        fatal "ACME_EMAIL is required. Export it or set it in your env."
    fi

    local tenants
    tenants=$(jq -c '.[]' "$TENANTS_FILE")

    if [[ -z "$tenants" ]]; then
        warn "No tenants registered. Generating infrastructure-only compose."
    fi

    # Pre-build per-tenant network entries for Traefik and network declarations
    local traefik_tenant_nets="" tenant_net_decls=""
    while IFS= read -r tenant; do
        [[ -z "$tenant" ]] && continue
        local u; u=$(echo "$tenant" | jq -r '.user')
        traefik_tenant_nets+="      net-${u}:"$'\n'
        tenant_net_decls+="  net-${u}:"$'\n'
    done <<< "$tenants"

    cat > "$COMPOSE_FILE" <<'HEADER'
# AUTO-GENERATED by alf multi-tenant provisioner.
# Do not edit manually. Regenerate with: generate-compose.sh

HEADER

    cat >> "$COMPOSE_FILE" <<EOF
name: alf-multi

services:
  traefik:
    image: traefik:v3.6
    container_name: alf-traefik
    restart: unless-stopped
    command:
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.websecure.address=:443"
      - "--entrypoints.web.http.redirections.entryPoint.to=websecure"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge=true"
      - "--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web"
      - "--certificatesresolvers.letsencrypt.acme.email=${ACME_EMAIL}"
      - "--certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme.json"
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./letsencrypt:/letsencrypt
    networks:
      default:
${traefik_tenant_nets}    userns_mode: "host"
    mem_limit: 256m
    cpus: "0.5"
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

  whisper:
    image: ${WHISPER_IMAGE}
    container_name: alf-whisper
    restart: unless-stopped
    networks:
      default:
      whisper-internal:
        ipv4_address: 10.99.0.10
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    environment:
      - WHISPER_SHARED_SECRET_FILE=/run/secrets/whisper_shared_secret
      - WHISPER_MODEL=${WHISPER_MODEL}
      - HF_HOME=/tmp/hf_cache
    volumes:
      - ./shared/models:/models
      - ./shared/whisper_shared_secret:/run/secrets/whisper_shared_secret:ro
    mem_limit: 2g
    cpus: "2.0"
    userns_mode: "host"
    logging:
      driver: json-file
      options:
        max-size: "20m"
        max-file: "3"

EOF

    # Emit one service block per tenant
    while IFS= read -r tenant; do
        [[ -z "$tenant" ]] && continue

        local user domain timezone image
        user=$(echo "$tenant" | jq -r '.user')
        domain=$(echo "$tenant" | jq -r '.domain')
        timezone=$(echo "$tenant" | jq -r '.timezone')
        image=$(echo "$tenant" | jq -r '.image')

        cat >> "$COMPOSE_FILE" <<EOF
  alf-${user}:
    image: ${image}
    container_name: alf-${user}
    restart: unless-stopped
    networks:
      net-${user}:
      whisper-internal:
    extra_hosts:
      - "whisper:10.99.0.10"
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=alf-multi_net-${user}"
      - "traefik.http.routers.alf-${user}.rule=Host(\`${domain}\`)"
      - "traefik.http.routers.alf-${user}.entrypoints=websecure"
      - "traefik.http.routers.alf-${user}.tls.certresolver=letsencrypt"
      - "traefik.http.services.alf-${user}.loadbalancer.server.port=8080"
      - "traefik.http.services.alf-${user}.loadbalancer.responseforwarding.flushinterval=-1"
      - "traefik.http.middlewares.alf-${user}-rl.ratelimit.average=60"
      - "traefik.http.middlewares.alf-${user}-rl.ratelimit.burst=20"
      - "traefik.http.routers.alf-${user}.middlewares=alf-${user}-rl"
    environment:
      - TELEGRAM_BOT_TOKEN_FILE=/run/secrets/telegram_bot_token
      - TELEGRAM_CHAT_ID_FILE=/run/secrets/telegram_chat_id
      - CC_AUTH_TOKEN_FILE=/run/secrets/cc_auth_token
      - OPENROUTER_API_KEY_FILE=/run/secrets/openrouter_api_key
      - OPENAI_API_KEY_FILE=/run/secrets/openai_api_key
      - CLAUDE_OAUTH_TOKEN_FILE=/run/secrets/claude_oauth_token
      - VAULT_MASTER_PASSWORD_FILE=/run/secrets/vault_master_password
      - WHISPER_URL=http://whisper:8000
      - WHISPER_SHARED_SECRET_FILE=/run/secrets/whisper_shared_secret
      - CC_EXTERNAL_URL=https://${domain}
      - TZ=${timezone}
    secrets:
      - source: ${user}_telegram_bot_token
        target: telegram_bot_token
      - source: ${user}_telegram_chat_id
        target: telegram_chat_id
      - source: ${user}_cc_auth_token
        target: cc_auth_token
      - source: ${user}_openrouter_api_key
        target: openrouter_api_key
      - source: ${user}_openai_api_key
        target: openai_api_key
      - source: ${user}_claude_oauth_token
        target: claude_oauth_token
      - source: ${user}_vault_master_password
        target: vault_master_password
      - source: ${user}_whisper_shared_secret
        target: whisper_shared_secret
        uid: "1000"
        gid: "1000"
        mode: 0400
    volumes:
      - ./tenants/${user}/data:/home/alf/data
      - ./tenants/${user}/config.d:/opt/alf/config.d
      - ./tenants/${user}/skills.d:/opt/alf/skills.d
      - ./tenants/${user}/cache/claude:/home/alf/.claude
      - ./tenants/${user}/cache/local:/home/alf/.local
      - ./tenants/${user}/cache/npm:/home/alf/.npm
      - ./tenants/${user}/cache/cache:/home/alf/.cache
      - ./tenants/${user}/local:/opt/alf/user-packages
      - ./tenants/${user}/vault-data:/opt/alf/vault-data
      - ./tenants/${user}/resolv.conf:/etc/resolv.conf
    mem_limit: 2g
    cpus: "2.0"
    runtime: \${ALF_RUNTIME:-runc}
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID
      - DAC_OVERRIDE
      - FOWNER
    logging:
      driver: json-file
      options:
        max-size: "50m"
        max-file: "5"

EOF
    done <<< "$tenants"

    # Networks
    cat >> "$COMPOSE_FILE" <<EOF
networks:
  whisper-internal:
    internal: true
    ipam:
      config:
        - subnet: 10.99.0.0/24
${tenant_net_decls}
EOF

    # Secrets section — per-tenant secrets with name mapping
    echo "secrets:" >> "$COMPOSE_FILE"
    while IFS= read -r tenant; do
        [[ -z "$tenant" ]] && continue
        local user
        user=$(echo "$tenant" | jq -r '.user')

        local secrets=(telegram_bot_token telegram_chat_id cc_auth_token openrouter_api_key
                       openai_api_key claude_oauth_token vault_master_password whisper_shared_secret)
        for s in "${secrets[@]}"; do
            cat >> "$COMPOSE_FILE" <<EOF
  ${user}_${s}:
    file: ./tenants/${user}/secrets/${s}
EOF
        done
    done <<< "$tenants"

    info "Generated $COMPOSE_FILE with $(jq length "$TENANTS_FILE") tenant(s)"
}
