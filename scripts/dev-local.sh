#!/usr/bin/env bash
set -euo pipefail

# Local development script: build and run ALF in Docker on this machine.
# Usage: ./scripts/dev-local.sh [--clean] [--fresh] [--no-frontend] [--down] [--stop]
#
# Flags:
#   --clean         Tear down everything first (volumes preserved)
#   --fresh         Wipe runtime data (config, data, cache) for a fresh install
#   --no-frontend   Skip Svelte frontend rebuild
#   --down          Stop and remove the stack, then exit
#   --stop          Stop containers without removing them, then exit
#
# 0.8.0 dev-window notice
# -----------------------
# Builds from release/0.8.0 have the legacy sandbox razed (ticket #406).
# The daemon refuses to start without ALF_EXPERIMENTAL=1, which this
# script sets automatically in the generated docker-compose.yml. The
# daemon will log a multi-line NO ISOLATION banner at boot and tag every
# Control Center response with `X-ALF-Experimental: no-isolation`.
# Do not use this script to deploy on shared or production hosts —
# stable releases track tags in release/0.7.x.

CLEAN=false
FRESH=false
NO_FRONTEND=false
DOWN=false
STOP=false

for arg in "$@"; do
  case "$arg" in
    --clean)       CLEAN=true ;;
    --fresh)       FRESH=true; CLEAN=true ;;
    --no-frontend) NO_FRONTEND=true ;;
    --down)        DOWN=true ;;
    --stop)        STOP=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

REPO_ROOT="$(git rev-parse --show-toplevel)"
LOCAL_DIR="$REPO_ROOT/dev-alf-local"

cd "$REPO_ROOT"

if [ "$DOWN" = true ]; then
  echo "==> Stopping ALF..."
  docker compose -f "$LOCAL_DIR/docker-compose.yml" down
  exit 0
fi

if [ "$STOP" = true ]; then
  echo "==> Stopping ALF containers (preserving them)..."
  docker compose -f "$LOCAL_DIR/docker-compose.yml" stop
  exit 0
fi

# All runtime data lives in dev-alf-local/ to keep the repo clean.
mkdir -p "$LOCAL_DIR"/{secrets,data,config.d,skills.d,cache/{claude,codex,local,npm,cache}}

# Bootstrap secrets if missing.
if [ ! -s "$LOCAL_DIR/secrets/cc_auth_token" ]; then
  echo "==> Creating dev-alf-local/secrets/ with placeholder values..."
  [ -s "$LOCAL_DIR/secrets/telegram_bot_token" ] || echo "REPLACE_ME" > "$LOCAL_DIR/secrets/telegram_bot_token"
  [ -s "$LOCAL_DIR/secrets/telegram_chat_id" ]   || echo "REPLACE_ME" > "$LOCAL_DIR/secrets/telegram_chat_id"
  openssl rand -hex 16 > "$LOCAL_DIR/secrets/cc_auth_token"
  echo "    Edit dev-alf-local/secrets/ with real values before first use."
fi
# Generate shared secrets for sidecars if missing.
for s in whisper_shared_secret embed_shared_secret; do
  [ -s "$LOCAL_DIR/secrets/$s" ] || openssl rand -hex 32 > "$LOCAL_DIR/secrets/$s"
done

if [ "$CLEAN" = true ]; then
  echo "==> Clean: tearing down existing stack..."
  docker compose -f "$LOCAL_DIR/docker-compose.yml" down --remove-orphans 2>/dev/null || true
fi

if [ "$FRESH" = true ]; then
  echo "==> Fresh: wiping runtime data..."
  # Bind-mounted dirs on host
  rm -rf "$LOCAL_DIR"/{config.d,data,cache}
  mkdir -p "$LOCAL_DIR"/{config.d,data,cache/{claude,codex,local,npm,cache}}
  # Named volumes for daemon-private storage (keys, vault, admin queue):
  # remove via docker so the fresh boot regenerates daemon key + bootstraps
  # vault from secrets-staging. Volumes are project-prefixed (alf-dev_*)
  # by docker compose; remove succeeds quietly on absent volumes.
  docker volume rm alf-dev_alf-keys alf-dev_alf-vault alf-dev_alf-admin 2>/dev/null || true
fi

# Generate docker-compose.yml in LOCAL_DIR with relative paths.
cat > "$LOCAL_DIR/docker-compose.yml" <<'COMPOSE'
name: alf-dev

services:
  alf:
    build:
      context: ..
      args:
        BUILD_VERSION: ${BUILD_VERSION:-dev-local}
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
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      - WHISPER_URL=http://whisper:8000
      - EMBED_URL=http://embed:8090
      - ALF_MARKETPLACE_URL=https://marketplace.alfos.ai
      - TZ=${TZ:-UTC}
      # 0.8.0 dev-window gate: the daemon refuses to start without this
      # after ticket #406 razed the legacy sandbox. Removed once
      # ALF_OCAP_STRICT=1 replaces it post-#391 + #386. Do not deploy this
      # build on shared or production hosts — see docs/ARCHITECTURE-SECURITY.md §12.
      - ALF_EXPERIMENTAL=1
    volumes:
      - ./secrets:/opt/alf/secrets-staging:ro
      - ./data:/home/alf/data
      # Daemon-private signing material (#395 §7.3 Tier 2 daemon key
      # auto-bootstrapped on first boot + Tier 3 user-endorsed key
      # written by `alf keygen`). NAMED VOLUME on purpose: bind-mounts
      # on Docker Desktop / macOS traverse a fakeowner FUSE layer that
      # ignores inode uid/gid, letting the LLM subprocess (uid 1000)
      # read alfd-owned 0o600 files. Named volumes stay inside the
      # Linux VM with real ext4 DAC, so 'cat /home/alf/data/keys/daemon.json'
      # from the LLM subprocess returns EACCES like it does on Linux native.
      # Operators can still inspect/back up via `docker run --rm
      # -v alf-keys:/k alpine tar czf - -C /k . > keys-backup.tar.gz`.
      - alf-keys:/home/alf/data/keys
      # Admin pending queue (#395 chunk 3) — DirStore items carry the
      # Item.Payload as JSON. Side-channel about what the LLM has been
      # asked to ratify; not LLM-readable. Same fakeowner reasoning.
      - alf-admin:/home/alf/data/admin
      - ./config.d:/opt/alf/config.d
      - ./skills.d:/opt/alf/skills.d
      - ./cache/claude:/home/alf/.claude
      - ./cache/codex:/home/alf/.codex
      - ./cache/local:/home/alf/.local
      - ./cache/npm:/home/alf/.npm
      - ./cache/cache:/home/alf/.cache
      # Vault store + bearer tokens + sidecar shared secrets (CC auth,
      # whisper, embed, telegram). All daemon-private. Named volume —
      # same fakeowner reasoning. The vault.sock chmod-on-socket also
      # works inside a Linux ext4 FS (fails on Docker Desktop bind).
      - alf-vault:/opt/alf/vault-data
    mem_limit: 2g
    cpus: "2.0"
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
    build: ../whisper-service
    container_name: alf-whisper
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
    volumes:
      - ./secrets/whisper_shared_secret:/run/secrets/whisper_shared_secret:ro
    mem_limit: 2g
    cpus: "2.0"

  embed:
    build:
      context: ..
      dockerfile: embed-service/Dockerfile
    container_name: alf-embed
    restart: unless-stopped
    networks:
      embed-internal:
        ipv4_address: 10.99.1.10
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    environment:
      - EMBED_SHARED_SECRET_FILE=/run/secrets/embed_shared_secret
    volumes:
      - ./secrets/embed_shared_secret:/run/secrets/embed_shared_secret:ro
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

volumes:
  # Named volumes for daemon-private storage. Stay inside the Docker
  # VM (Linux ext4) so DAC enforces alfd:alfd 0o600 properly even on
  # Docker Desktop hosts. See docs/ARCHITECTURE-SECURITY.md §7.3 for
  # the trust-surface boundary these volumes implement.
  alf-keys:
  alf-vault:
  alf-admin:
COMPOSE

# Build frontend unless skipped.
if [ "$NO_FRONTEND" = true ]; then
  echo "==> Skipping frontend build (--no-frontend)"
else
  echo "==> Building frontend (Svelte)..."
  (cd internal/controlcenter/frontend && npm install --silent && npm run build)
fi

# Set timezone from host if not already set.
export TZ="${TZ:-$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || echo UTC)}"

BUILD_VERSION=$(git describe --tags --always 2>/dev/null || echo "dev-local")
export BUILD_VERSION

echo "==> Building ALF CLI (${BUILD_VERSION})..."
go build -ldflags "-s -w -X main.version=${BUILD_VERSION}" -o "$LOCAL_DIR/alf" ./cmd/alf/

# Symlink so the CLI finds secrets at the expected path.
ln -sf "$LOCAL_DIR/secrets" "$LOCAL_DIR/secrets-link" 2>/dev/null || true

echo "==> Building Docker image + starting stack..."
docker compose -f "$LOCAL_DIR/docker-compose.yml" up --build -d

echo "==> Waiting for health check..."
for i in $(seq 1 20); do
  if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
    echo "==> ALF is running at http://localhost:8080"
    ALF_DIR="$LOCAL_DIR" "$LOCAL_DIR/alf" magic-link 2>/dev/null || echo "    (magic-link failed — try: ALF_DIR=$LOCAL_DIR $LOCAL_DIR/alf magic-link)"
    echo "    Logs: docker compose -f $LOCAL_DIR/docker-compose.yml logs -f"
    exit 0
  fi
  sleep 1
done

echo "==> Daemon not healthy yet. Check logs: docker compose -f $LOCAL_DIR/docker-compose.yml logs -f"
