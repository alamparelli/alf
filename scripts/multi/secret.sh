#!/usr/bin/env bash
# Manage secrets for a specific tenant.
# Usage:
#   secret.sh --user <name> set <secret_name> <value>
#   secret.sh --user <name> list
#
# Example:
#   ./secret.sh --user alice set openrouter_api_key sk-or-...
#   ./secret.sh --user alice set telegram_bot_token 123456:ABC...
#   ./secret.sh --user alice list

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

USER="" ACTION="" SECRET_NAME="" SECRET_VALUE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --user) USER="$2"; shift 2 ;;
        set)
            ACTION="set"
            SECRET_NAME="${2:-}"; SECRET_VALUE="${3:-}"
            shift; [[ -n "$SECRET_NAME" ]] && shift; [[ -n "$SECRET_VALUE" ]] && shift
            ;;
        list) ACTION="list"; shift ;;
        *) fatal "Unknown arg: $1" ;;
    esac
done

[[ -z "$USER" ]]   && fatal "Missing --user"
[[ -z "$ACTION" ]] && fatal "Missing action (set|list)"

ensure_registry

if ! tenant_exists "$USER"; then
    fatal "Tenant '$USER' not found."
fi

SECRETS_DIR="$TENANTS_DIR/$USER/secrets"

case "$ACTION" in
    set)
        [[ -z "$SECRET_NAME" ]]  && fatal "Missing secret name"
        [[ -z "$SECRET_VALUE" ]] && fatal "Missing secret value"

        echo -n "$SECRET_VALUE" > "$SECRETS_DIR/$SECRET_NAME"
        chmod 644 "$SECRETS_DIR/$SECRET_NAME"
        info "Secret '$SECRET_NAME' set for tenant '$USER'"
        echo
        warn "Restart the tenant to apply: docker compose -f $COMPOSE_FILE restart alf-$USER"
        ;;
    list)
        echo
        printf "  ${BOLD}%-30s %s${RESET}\n" "SECRET" "STATUS"
        printf "  %-30s %s\n" "──────" "──────"

        for f in "$SECRETS_DIR"/*; do
            [[ ! -f "$f" ]] && continue
            name=$(basename "$f")
            if [[ -s "$f" ]]; then
                printf "  %-30s ${GREEN}set${RESET}\n" "$name"
            else
                printf "  %-30s ${DIM}unset${RESET}\n" "$name"
            fi
        done
        echo
        ;;
esac
