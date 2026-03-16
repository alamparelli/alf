#!/usr/bin/env bash
# List all provisioned ALF tenants.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

ensure_registry

count=$(jq length "$TENANTS_FILE")

if [[ "$count" -eq 0 ]]; then
    echo "No tenants provisioned."
    exit 0
fi

echo
printf "  ${BOLD}%-15s %-35s %-15s %-10s %s${RESET}\n" "USER" "DOMAIN" "TIMEZONE" "STATUS" "CREATED"
printf "  %-15s %-35s %-15s %-10s %s\n" "────" "──────" "────────" "──────" "───────"

while IFS=$'\t' read -r user domain timezone image created; do
    # Check container status
    status=$(docker inspect --format='{{.State.Status}}' "alf-$user" 2>/dev/null || echo "absent")
    case "$status" in
        running) color="$GREEN" ;;
        exited)  color="$YELLOW" ;;
        *)       color="$RED" ;;
    esac
    printf "  %-15s %-35s %-15s ${color}%-10s${RESET} %s\n" "$user" "$domain" "$timezone" "$status" "$created"
done < <(list_tenants)

echo
echo "  Total: $count tenant(s)"
echo
