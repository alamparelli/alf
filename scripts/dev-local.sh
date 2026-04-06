#!/usr/bin/env bash
set -euo pipefail

# Local development script: build and run ALF in Docker on this machine.
# Usage: ./scripts/dev-local.sh [--clean] [--no-frontend] [--down]
#
# Flags:
#   --clean         Tear down everything first (volumes preserved)
#   --no-frontend   Skip Svelte frontend rebuild
#   --down          Stop the stack and exit

CLEAN=false
NO_FRONTEND=false
DOWN=false

for arg in "$@"; do
  case "$arg" in
    --clean)       CLEAN=true ;;
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

# Bootstrap dev-secrets if missing.
if [ ! -d dev-secrets ]; then
  echo "==> Creating dev-secrets/ with placeholder values..."
  mkdir -p dev-secrets
  echo "REPLACE_ME" > dev-secrets/telegram_bot_token
  echo "REPLACE_ME" > dev-secrets/telegram_chat_id
  openssl rand -hex 16 > dev-secrets/cc_auth_token
  echo "    Edit dev-secrets/ with real values before first use."
fi

if [ "$CLEAN" = true ]; then
  echo "==> Clean: tearing down existing stack..."
  docker compose down --remove-orphans 2>/dev/null || true
fi

# Build frontend unless skipped.
if [ "$NO_FRONTEND" = true ]; then
  echo "==> Skipping frontend build (--no-frontend)"
else
  echo "==> Building frontend (Svelte)..."
  (cd internal/controlcenter/frontend && npm install --silent && npm run build)
fi

echo "==> Building Docker image + starting stack..."
docker compose up --build -d

echo "==> Waiting for health check..."
for i in $(seq 1 20); do
  if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
    echo "==> ALF is running at http://localhost:8080"
    TOKEN=$(cat dev-secrets/cc_auth_token)
    echo "    Auth token: ${TOKEN}"
    echo "    Logs: docker compose logs -f"
    exit 0
  fi
  sleep 1
done

echo "==> Daemon not healthy yet. Check logs: docker compose logs -f"
