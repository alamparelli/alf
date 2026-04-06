#!/usr/bin/env bash
set -euo pipefail

# Local development script: build and run ALF in Docker on this machine.
# Usage: ./scripts/dev-local.sh [--clean] [--fresh] [--no-frontend] [--down]
#
# Flags:
#   --clean         Tear down everything first (volumes preserved)
#   --fresh         Wipe runtime data (config, data, cache) for a fresh install
#   --no-frontend   Skip Svelte frontend rebuild
#   --down          Stop the stack and exit

CLEAN=false
FRESH=false
NO_FRONTEND=false
DOWN=false

for arg in "$@"; do
  case "$arg" in
    --clean)       CLEAN=true ;;
    --fresh)       FRESH=true; CLEAN=true ;;
    --no-frontend) NO_FRONTEND=true ;;
    --down)        DOWN=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"

if [ "$DOWN" = true ]; then
  echo "==> Stopping ALF..."
  docker compose down
  exit 0
fi

# Bootstrap directories.
mkdir -p dev-secrets dev-data dev-config.d dev-skills.d dev-vault-data \
  dev-cache/{claude,codex,local,npm,cache}

# Bootstrap dev-secrets if missing.
if [ ! -s dev-secrets/cc_auth_token ]; then
  echo "==> Creating dev-secrets/ with placeholder values..."
  [ -s dev-secrets/telegram_bot_token ] || echo "REPLACE_ME" > dev-secrets/telegram_bot_token
  [ -s dev-secrets/telegram_chat_id ]   || echo "REPLACE_ME" > dev-secrets/telegram_chat_id
  openssl rand -hex 16 > dev-secrets/cc_auth_token
  echo "    Edit dev-secrets/ with real values before first use."
fi
# Generate shared secrets for sidecars if missing.
for s in whisper_shared_secret embed_shared_secret; do
  [ -s "dev-secrets/$s" ] || openssl rand -hex 32 > "dev-secrets/$s"
done

if [ "$CLEAN" = true ]; then
  echo "==> Clean: tearing down existing stack..."
  docker compose down --remove-orphans 2>/dev/null || true
fi

if [ "$FRESH" = true ]; then
  echo "==> Fresh: wiping runtime data..."
  rm -rf dev-config.d dev-data dev-vault-data dev-cache
  mkdir -p dev-config.d dev-data dev-vault-data dev-cache/{claude,codex,local,npm,cache}
fi

# Build frontend unless skipped.
if [ "$NO_FRONTEND" = true ]; then
  echo "==> Skipping frontend build (--no-frontend)"
else
  echo "==> Building frontend (Svelte)..."
  (cd internal/controlcenter/frontend && npm install --silent && npm run build)
fi

# Set timezone from host if not already set.
export TZ="${TZ:-$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || echo UTC)}"

echo "==> Building ALF CLI..."
go build -ldflags "-s -w -X main.version=dev-local" -o ./dev-alf ./cmd/alf/

# Symlink so the CLI finds secrets at the expected path.
ln -sf dev-secrets secrets 2>/dev/null || true

echo "==> Building Docker image + starting stack..."
docker compose up --build -d

echo "==> Waiting for health check..."
for i in $(seq 1 20); do
  if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
    echo "==> ALF is running at http://localhost:8080"
    ALF_DIR=. ./dev-alf magic-link 2>/dev/null || echo "    (magic-link failed — try: ALF_DIR=. ./dev-alf magic-link)"
    echo "    Logs: docker compose logs -f"
    exit 0
  fi
  sleep 1
done

echo "==> Daemon not healthy yet. Check logs: docker compose logs -f"
