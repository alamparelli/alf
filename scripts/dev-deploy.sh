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

echo "==> Pruning old ${IMAGE_NAME} images..."
docker images "${IMAGE_NAME}" --format '{{.ID}} {{.Tag}}' | grep -v latest | awk '{print $1}' | xargs -r docker rmi 2>/dev/null || true

echo "==> Building CLI (linux/amd64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags "-s -w -X main.version=${TAG}" \
  -o /tmp/alf-deploy \
  ./cmd/alf/

echo "==> Transferring CLI binary to homelab..."
$SCP /tmp/alf-deploy "${REMOTE_HOST}:/tmp/alf-bin"
$SSH "${REMOTE_HOST}" "sudo install -m 755 /tmp/alf-bin /usr/local/bin/alf && install -m 755 /tmp/alf-bin /home/alessandro/.local/bin/alf && rm /tmp/alf-bin"

echo "==> Vendoring vault-proxy source..."
rm -rf third_party/vault-proxy
mkdir -p third_party/vault-proxy
rsync -a --exclude .git --exclude vault-data --exclude '/vault-server' --exclude '/vault-cli' \
  ../Projects/vault-proxy/ third_party/vault-proxy/

echo "==> Syncing source to homelab for native build..."
rsync -az --delete \
  -e "ssh -S $SSH_SOCK" \
  --exclude .git --exclude node_modules --exclude mobile --exclude .claude \
  ./ "${REMOTE_HOST}:/tmp/alf-build/"

rm -rf third_party/vault-proxy

echo "==> Building Docker image natively on homelab..."
$SSH "${REMOTE_HOST}" "cd /tmp/alf-build && docker build -t '${FULL_TAG}' -t '${IMAGE_NAME}:latest' ."

echo "==> Building whisper-service image on homelab..."
$SSH "${REMOTE_HOST}" "cd /tmp/alf-build/whisper-service && docker build -t alf-whisper:latest ."

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
      - "traefik.http.routers.alf-cc.rule=Host(\`cc.lamparelli.eu\`)"
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
# Auto-generate whisper shared secret if missing.
if [ ! -s ${REMOTE_DIR}/secrets/whisper_shared_secret ]; then
  openssl rand -hex 32 > ${REMOTE_DIR}/secrets/whisper_shared_secret
  echo \"  generated whisper_shared_secret\"
fi
# Ensure all secret files exist (Docker Compose requires them even if empty).
for s in telegram_bot_token telegram_chat_id cc_auth_token openrouter_api_key vault_master_password whisper_shared_secret; do
  touch ${REMOTE_DIR}/secrets/\$s
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
    restart: unless-stopped
    networks:
      - default
      - whisper-internal
    expose:
      - "8080"
    environment:
      - TELEGRAM_BOT_TOKEN_FILE=/run/secrets/telegram_bot_token
      - TELEGRAM_CHAT_ID_FILE=/run/secrets/telegram_chat_id
      - CC_AUTH_TOKEN_FILE=/run/secrets/cc_auth_token
      - OPENROUTER_API_KEY_FILE=/run/secrets/openrouter_api_key
      - VAULT_MASTER_PASSWORD_FILE=/run/secrets/vault_master_password
      - WHISPER_URL=http://whisper:8000
      - WHISPER_SHARED_SECRET_FILE=/run/secrets/whisper_shared_secret
      - TZ=Europe/Rome
    secrets:
      - telegram_bot_token
      - telegram_chat_id
      - cc_auth_token
      - openrouter_api_key
      - vault_master_password
      - source: whisper_shared_secret
        target: /run/secrets/whisper_shared_secret
        uid: "1000"
        gid: "1000"
        mode: 0400
    volumes:
      - ./data:/home/alf/data
      - ./config.d:/opt/alf/config.d
      - ./skills.d:/opt/alf/skills.d
      - ./cache/claude:/home/alf/.claude
      - ./cache/local:/home/alf/.local
      - ./cache/npm:/home/alf/.npm
      - ./cache/cache:/home/alf/.cache
      - ./local:/opt/alf/user-packages
      - ./vault-data:/opt/alf/vault-data
    mem_limit: 2g
    cpus: "2.0"
    runtime: ${ALF_RUNTIME:-runc}
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

  whisper:
    image: alf-whisper:latest
    container_name: alf-whisper
    pull_policy: never
    restart: unless-stopped
    networks:
      - whisper-internal
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    environment:
      - WHISPER_SHARED_SECRET_FILE=/run/secrets/whisper_shared_secret
      - WHISPER_MODEL=small
    secrets:
      - whisper_shared_secret
    volumes:
      - ./data/models:/models
    mem_limit: 2g
    cpus: "2.0"

networks:
  whisper-internal:
    internal: true

secrets:
  telegram_bot_token:
    file: ./secrets/telegram_bot_token
  telegram_chat_id:
    file: ./secrets/telegram_chat_id
  cc_auth_token:
    file: ./secrets/cc_auth_token
  openrouter_api_key:
    file: ./secrets/openrouter_api_key
  vault_master_password:
    file: ./secrets/vault_master_password
  whisper_shared_secret:
    file: ./secrets/whisper_shared_secret
COMPOSEOF
$SCP /tmp/alf-compose-direct.yml "${REMOTE_HOST}:${REMOTE_DIR}/docker-compose.yml"
rm -f /tmp/alf-compose-direct.yml

if [ "$NO_RESTART" = true ]; then
  echo "==> Image transferred. Skipping restart (--no-restart)."
else
  echo "==> Restarting ALF on homelab..."
  $SSH "${REMOTE_HOST}" "cd ${REMOTE_DIR} && docker compose up -d"
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
