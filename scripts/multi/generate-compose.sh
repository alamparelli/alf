#!/usr/bin/env bash
# Regenerate docker-compose.yml from tenants registry.
# Useful after manually editing tenants.json or changing env vars.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

ensure_registry
generate_compose

echo
info "To apply changes:"
echo "  cd $ALF_MULTI_DIR && docker compose up -d"
echo
