#!/usr/bin/env bash
set -euo pipefail

# Dev deployment script: build locally, deploy to homelab via SSH.
# Usage: ./scripts/dev-deploy.sh [--no-restart] [--clean]

REMOTE_HOST="alessandro@192.168.129.101"
REMOTE_DIR="/home/alessandro/alf2"
IMAGE_NAME="alf-homelab"

NO_RESTART=false
CLEAN=false

for arg in "$@"; do
  case "$arg" in
    --no-restart) NO_RESTART=true ;;
    --clean)      CLEAN=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"

# SSH multiplexing — single passphrase prompt for all connections
SSH_SOCK="/tmp/alf-deploy-ssh-$$"
ssh -fNM -S "$SSH_SOCK" "$REMOTE_HOST"
cleanup() { ssh -S "$SSH_SOCK" -O exit "$REMOTE_HOST" 2>/dev/null || true; }
trap cleanup EXIT

SSH="ssh -S $SSH_SOCK"
SCP="scp -o ControlPath=$SSH_SOCK"

GIT_HASH=$(git rev-parse --short HEAD)
TAG="dev-${GIT_HASH}"
FULL_TAG="${IMAGE_NAME}:${TAG}"
BUILD_VERSION=$(git describe --tags --always 2>/dev/null || echo "${TAG}")

echo "==> Pruning old ${IMAGE_NAME} images..."
docker images "${IMAGE_NAME}" --format '{{.ID}} {{.Tag}}' | grep -v latest | awk '{print $1}' | xargs -r docker rmi 2>/dev/null || true

echo "==> Building frontend (Svelte)..."
(cd internal/controlcenter/frontend && npm install --silent && npm run build)

echo "==> Building CLI (linux/amd64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags "-s -w -X main.version=${BUILD_VERSION}" \
  -o /tmp/alf-deploy \
  ./cmd/alf/

echo "==> Transferring CLI binary to homelab..."
$SCP /tmp/alf-deploy "${REMOTE_HOST}:/tmp/alf-bin"
$SSH "${REMOTE_HOST}" "sudo install -m 755 /tmp/alf-bin /usr/local/bin/alf && install -m 755 /tmp/alf-bin /home/alessandro/.local/bin/alf && rm /tmp/alf-bin"

VAULT_PROXY_SRC="${VAULT_PROXY_SRC:-$HOME/Dev/Projects/vault-proxy}"
VAULT_PROXY_DEST="internal/controlcenter/frontend/third_party/vault-proxy"
echo "==> Vendoring vault-proxy source from ${VAULT_PROXY_SRC}..."
test -d "${VAULT_PROXY_SRC}" || { echo "ERROR: vault-proxy not found at ${VAULT_PROXY_SRC}"; exit 1; }
rm -rf "${VAULT_PROXY_DEST}"
mkdir -p "${VAULT_PROXY_DEST}"
rsync -a --exclude .git --exclude vault-data --exclude '/vault-server' --exclude '/vault-cli' \
  "${VAULT_PROXY_SRC}/" "${VAULT_PROXY_DEST}/"

echo "==> Syncing source to homelab for native build..."
rsync -az --delete \
  -e "ssh -S $SSH_SOCK" \
  --exclude .git --exclude node_modules --exclude mobile --exclude .claude \
  ./ "${REMOTE_HOST}:/tmp/alf-build/"

echo "==> Building Docker image natively on homelab..."
$SSH "${REMOTE_HOST}" "cd /tmp/alf-build && docker build --build-arg BUILD_VERSION='${BUILD_VERSION}' -t '${FULL_TAG}' -t '${IMAGE_NAME}:latest' ."

echo "==> Building whisper-service image on homelab..."
$SSH "${REMOTE_HOST}" "cd /tmp/alf-build/whisper-service && docker build -t alf-whisper:latest ."

echo "==> Building embed-service image on homelab..."
$SSH "${REMOTE_HOST}" "cd /tmp/alf-build && docker build -f embed-service/Dockerfile -t alf-embed:latest ."

$SSH "${REMOTE_HOST}" "rm -rf /tmp/alf-build"

# Generate Traefik override locally then SCP to remote
echo "==> Generating docker-compose.override.yml..."
cat > /tmp/alf-compose-override.yml <<OVERRIDE
services:
  alf:
    image: ${FULL_TAG}
    pull_policy: never
    networks:
      - default
      - proxy
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.alf-cc.rule=Host(\`cc.lamparelli.eu\`) && !PathPrefix(\`/s/\`) && !PathPrefix(\`/alpha/\`)"
      - "traefik.http.routers.alf-cc.priority=200"
      - "traefik.http.routers.alf-cc.entrypoints=websecure"
      - "traefik.http.routers.alf-cc.tls.certresolver=myresolver"
      - "traefik.http.services.alf-cc.loadbalancer.server.port=8080"
      - "traefik.http.services.alf-cc.loadbalancer.responseforwarding.flushinterval=-1"
      - "traefik.docker.network=proxy"
    environment:
      - CC_EXTERNAL_URL=https://cc.lamparelli.eu

networks:
  proxy:
    external: true
OVERRIDE

# Bootstrap directory structure (simulates alf init for fresh installs).
echo "==> Bootstrapping remote directory..."
$SSH "${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}/{data/agents/teams,config.d,skills.d,secrets,local,vault-data} ${REMOTE_DIR}/cache/{claude,local,npm,cache}"

# Copy secrets from existing alf install, or create placeholders.
EXISTING_ALF="/home/alessandro/alf"
$SSH "${REMOTE_HOST}" "if [ -d ${EXISTING_ALF}/secrets ]; then
  for s in telegram_bot_token telegram_chat_id cc_auth_token openrouter_api_key vault_master_password; do
    if [ ! -s ${REMOTE_DIR}/secrets/\$s ] && [ -s ${EXISTING_ALF}/secrets/\$s ]; then
      cp ${EXISTING_ALF}/secrets/\$s ${REMOTE_DIR}/secrets/\$s
      echo \"  copied secret: \$s\"
    fi
  done
fi
# Auto-generate shared secrets if missing.
for s in whisper_shared_secret embed_shared_secret; do
  if [ ! -s ${REMOTE_DIR}/secrets/\$s ]; then
    openssl rand -hex 32 > ${REMOTE_DIR}/secrets/\$s
    chmod 600 ${REMOTE_DIR}/secrets/\$s
    echo \"  generated \$s\"
  fi
done
# Ensure required secret files exist (Docker Compose requires them even if empty).
for s in claude_oauth_token whisper_shared_secret embed_shared_secret; do
  touch ${REMOTE_DIR}/secrets/\$s
  chmod 600 ${REMOTE_DIR}/secrets/\$s
done"

$SCP /tmp/alf-compose-override.yml "${REMOTE_HOST}:${REMOTE_DIR}/docker-compose.override.yml"
rm -f /tmp/alf-compose-override.yml

echo "==> Syncing bundled skills..."
rsync -az -e "ssh -S $SSH_SOCK" \
  ./skills.d/ "${REMOTE_HOST}:${REMOTE_DIR}/skills.d/"

echo "==> Syncing bundled agent teams..."
rsync -az -e "ssh -S $SSH_SOCK" \
  ./internal/cli/bundled_agents/ "${REMOTE_HOST}:${REMOTE_DIR}/data/agents/teams/"

if [ "$CLEAN" = true ]; then
  echo "==> Clean: tearing down existing deployment..."
  $SSH "${REMOTE_HOST}" "cd ${REMOTE_DIR} && docker compose down --remove-orphans 2>/dev/null || true"
fi

# Always generate docker-compose.yml directly for dev deploys.
# Avoids inheriting the host's setup profile (which may have HTTPS/traefik enabled).
# The override adds traefik labels for the external proxy stack.
echo "==> Generating docker-compose.yml..."
cat > /tmp/alf-compose-direct.yml <<'COMPOSEOF'
name: alf-go

services:
  alf:
    image: alf-homelab:latest
    pull_policy: never
    container_name: alf
    hostname: workspace
    restart: unless-stopped
    networks:
      default:
      whisper-internal:
      embed-internal:
    extra_hosts:
      - "whisper:10.99.0.10"
      - "embed:10.99.1.10"
    expose:
      - "8080"
    environment:
      - CLAUDE_OAUTH_TOKEN_FILE=/run/secrets/claude_oauth_token
      - WHISPER_URL=http://whisper:8000
      - WHISPER_SHARED_SECRET_FILE=/run/secrets/whisper_shared_secret
      - EMBED_URL=http://embed:8090
      - EMBED_SHARED_SECRET_FILE=/run/secrets/embed_shared_secret
      - ALF_MARKETPLACE_URL=https://marketplace.lamparelli.eu
      - TZ=Europe/Rome
    secrets:
      - claude_oauth_token
      - whisper_shared_secret
      - embed_shared_secret
    volumes:
      - ./data:/home/alf/data
      - ./config.d:/opt/alf/config.d
      - ./skills.d:/opt/alf/skills.d
      - ./cache/claude:/home/alf/.claude
      - ./cache/codex:/home/alf/.codex
      - ./cache/config:/home/alf/.config
      - ./cache/local:/home/alf/.local
      - ./cache/npm:/home/alf/.npm
      - ./cache/cache:/home/alf/.cache
      - ./local:/opt/alf/user-packages
      - ./vault-data:/opt/alf/vault-data
      - ./resolv.conf:/etc/resolv.conf:ro
      - /OBSIDIAN:/home/alf/data/external/obsidian
      - /mnt/DATA/ALF:/home/alf/data/external/alf-storage
    mem_limit: 2g
    cpus: "2.0"
    runtime: ${ALF_RUNTIME:-runc}
    security_opt:
      - apparmor=unconfined
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID
      - SYS_ADMIN
      - SYS_CHROOT
      - DAC_OVERRIDE
      - FOWNER
      - NET_ADMIN

  whisper:
    image: alf-whisper:latest
    container_name: alf-whisper
    pull_policy: never
    restart: unless-stopped
    networks:
      whisper-internal:
        ipv4_address: 10.99.0.10
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    environment:
      - WHISPER_SHARED_SECRET_FILE=/run/secrets/whisper_shared_secret
      - WHISPER_MODEL=small
    secrets:
      - whisper_shared_secret
    mem_limit: 2g
    cpus: "2.0"

  embed:
    image: alf-embed:latest
    container_name: alf-embed
    pull_policy: never
    restart: unless-stopped
    profiles:
      - embed
    networks:
      embed-internal:
        ipv4_address: 10.99.1.10
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    environment:
      - EMBED_SHARED_SECRET_FILE=/run/secrets/embed_shared_secret
    secrets:
      - embed_shared_secret
    mem_limit: 768m
    cpus: "1.0"

networks:
  whisper-internal:
    internal: true
    ipam:
      config:
        - subnet: 10.99.0.0/24
  embed-internal:
    internal: true
    ipam:
      config:
        - subnet: 10.99.1.0/24

secrets:
  claude_oauth_token:
    file: ./secrets/claude_oauth_token
  whisper_shared_secret:
    file: ./secrets/whisper_shared_secret
  embed_shared_secret:
    file: ./secrets/embed_shared_secret
COMPOSEOF
$SCP /tmp/alf-compose-direct.yml "${REMOTE_HOST}:${REMOTE_DIR}/docker-compose.yml"
rm -f /tmp/alf-compose-direct.yml

# Force runc runtime — gVisor (runsc) is incompatible with PTY/terminal (xterm.js).
echo "==> Setting container runtime to runc..."
$SSH "${REMOTE_HOST}" "echo 'ALF_RUNTIME=runc' > ${REMOTE_DIR}/.env"

# Write resolv.conf for gVisor DNS compatibility.
$SSH "${REMOTE_HOST}" "echo -e 'nameserver 8.8.8.8\nnameserver 1.1.1.1' > ${REMOTE_DIR}/resolv.conf"

if [ "$NO_RESTART" = true ]; then
  echo "==> Image transferred. Skipping restart (--no-restart)."
else
  echo "==> Restarting ALF on homelab..."
  $SSH "${REMOTE_HOST}" "cd ${REMOTE_DIR} && docker compose --profile embed up -d"
  echo "==> Waiting for daemon startup..."
  for i in $(seq 1 20); do
    if $SSH "${REMOTE_HOST}" "cd ${REMOTE_DIR} && docker compose exec -T alf curl -sf http://localhost:8080/health" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  echo "==> Generating magic link..."
  $SSH "${REMOTE_HOST}" "cd ${REMOTE_DIR} && alf magic-link" || echo "  (magic-link failed — daemon may still be starting, run 'alf magic-link' later)"
  echo ""
  echo "==> Done. View logs with:"
  echo "    ssh ${REMOTE_HOST} 'cd ${REMOTE_DIR} && docker compose logs -f'"
fi
