#!/usr/bin/env bash
# Build the self-extracting multi-tenant installer.
# Output: dist/setup.sh (the curl-pipe-sh installer)
#
# The installer is designed to be downloaded as a file first (not piped),
# because it contains an embedded payload after __BUNDLE__.
#
# Usage: ./bundle.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MULTI_DIR="$SCRIPT_DIR/../multi"
DIST_DIR="$SCRIPT_DIR/dist"

mkdir -p "$DIST_DIR"

# Create a tar.gz of all multi scripts + bundled defaults
REPO_ROOT="$SCRIPT_DIR/../.."
STAGING=$(mktemp -d)

# Scripts
cp "$MULTI_DIR"/{lib,init,provision,teardown,upgrade,list,magic-link,secret,generate-compose,uninstall,apply}.sh "$STAGING/"

# Bundled defaults (for upgrade.sh tenant seeding)
mkdir -p "$STAGING/defaults/skills.d" "$STAGING/defaults/agents"
cp -r "$REPO_ROOT/skills.d"/*/ "$STAGING/defaults/skills.d/" 2>/dev/null || true
cp "$REPO_ROOT/internal/cli/bundled_agents"/*.json "$STAGING/defaults/agents/" 2>/dev/null || true

BUNDLE=$(mktemp)
tar --no-xattrs -czf "$BUNDLE" -C "$STAGING" .

BUNDLE_B64=$(base64 < "$BUNDLE")
rm -f "$BUNDLE"
rm -rf "$STAGING"

# Write the bootstrap script (what gets curl-piped).
# This downloads the full self-extracting script, then runs it.
cat > "$DIST_DIR/setup.sh" <<'BOOTSTRAP'
#!/bin/sh
set -e

# Infrastructure provisioner (private distribution)
# Usage: curl -fsSL https://cc.lamparelli.eu/s/setup.sh | S_TOKEN=<token> sh

BASE_URL="https://cc.lamparelli.eu/s"

if [ -z "${S_TOKEN:-}" ]; then
    printf "S_TOKEN required. Usage:\n\n"
    printf "  curl -fsSL %s/setup.sh | S_TOKEN=<token> sh\n\n" "$BASE_URL"
    exit 1
fi

# Download the full payload (auth-protected)
TMPSCRIPT=$(mktemp)
http_code=$(curl -fsSL -o "$TMPSCRIPT" -w '%{http_code}' -u "s:${S_TOKEN}" "${BASE_URL}/payload.sh" 2>/dev/null) || true

if [ ! -s "$TMPSCRIPT" ] || [ "$http_code" = "401" ] || [ "$http_code" = "403" ]; then
    echo "Authentication failed. Check your S_TOKEN."
    rm -f "$TMPSCRIPT"
    exit 1
fi

chmod +x "$TMPSCRIPT"
S_TOKEN="$S_TOKEN" sh "$TMPSCRIPT"
rm -f "$TMPSCRIPT"
BOOTSTRAP

# Write the actual payload (self-extracting, runs as a file)
cat > "$DIST_DIR/payload.sh" <<'HEADER'
#!/bin/sh
set -e

INSTALL_DIR="${INSTALL_DIR:-/opt/alf-multi}"
SCRIPTS_DIR="${INSTALL_DIR}/bin"

main() {
    # Check dependencies
    for cmd in docker jq openssl curl; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            echo "Missing dependency: $cmd"
            exit 1
        fi
    done

    # Verify docker compose plugin
    if ! docker compose version >/dev/null 2>&1; then
        echo "Missing: docker compose plugin (v2)"
        exit 1
    fi

    echo "Setting up infrastructure tools..."

    # Create directories
    sudo mkdir -p "$INSTALL_DIR" "$SCRIPTS_DIR"
    sudo chown "$(id -u):$(id -g)" "$INSTALL_DIR" "$SCRIPTS_DIR"

    # Extract embedded bundle
    tmpdir=$(mktemp -d)
    BUNDLE_LINE=$(grep -n '^__BUNDLE__$' "$0" | head -1 | cut -d: -f1)
    tail -n +"$((BUNDLE_LINE + 1))" "$0" | base64 -d | tar -xzf - -C "$tmpdir"

    # Install scripts
    cp "$tmpdir"/*.sh "$SCRIPTS_DIR/"
    chmod +x "$SCRIPTS_DIR"/*.sh

    # Install bundled defaults (skills, agents) for upgrade seeding
    if [ -d "$tmpdir/defaults" ]; then
        sudo mkdir -p "$INSTALL_DIR/defaults"
        sudo cp -r "$tmpdir/defaults"/* "$INSTALL_DIR/defaults/"
        sudo chown -R "$(id -u):$(id -g)" "$INSTALL_DIR/defaults"
    fi

    rm -rf "$tmpdir"

    # Save token for future updates
    if [ -n "${S_TOKEN:-}" ]; then
        echo "$S_TOKEN" > "$INSTALL_DIR/.s_token"
        chmod 600 "$INSTALL_DIR/.s_token"
    fi

    echo ""
    echo "Installed to: $SCRIPTS_DIR"
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "  ALF Multi-Tenant Management"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "  Setup"
    echo "    init.sh                First-time infrastructure setup"
    echo "                           Requires: ACME_EMAIL=you@example.com"
    echo ""
    echo "  Tenant lifecycle"
    echo "    provision.sh           Add a new tenant"
    echo "      --user <name> --domain <fqdn> [--timezone <tz>] [--image <tag>]"
    echo "    teardown.sh            Remove a tenant"
    echo "      --user <name> [--keep-data]"
    echo "    magic-link.sh          Generate login link"
    echo "      --user <name>"
    echo ""
    echo "  Operations"
    echo "    upgrade.sh             Upgrade all tenants (pull, permissions, seed, restart)"
    echo "      [--skip-pull] [--no-restart]"
    echo "    apply.sh               Regenerate compose and restart"
    echo "      [--pull]"
    echo "    list.sh                Show all tenants and status"
    echo "    secret.sh              Manage tenant secrets"
    echo "      --user <name> set <key> <value>"
    echo "      --user <name> list"
    echo ""
    echo "  Maintenance"
    echo "    generate-compose.sh    Regenerate docker-compose.yml only"
    echo "    uninstall.sh           Remove everything"
    echo "      [--keep-data]"
    echo ""
    echo "  Quick start:"
    echo "    export ACME_EMAIL=you@example.com"
    echo "    $SCRIPTS_DIR/init.sh"
    echo "    $SCRIPTS_DIR/provision.sh --user alice --domain alice.example.com"
    echo ""
}

main
exit 0
__BUNDLE__
HEADER

# Append the base64 bundle to the payload
echo "$BUNDLE_B64" >> "$DIST_DIR/payload.sh"

chmod +x "$DIST_DIR/setup.sh" "$DIST_DIR/payload.sh"

# Create the auth probe file
echo "ok" > "$DIST_DIR/probe"

echo "Built: $DIST_DIR/setup.sh ($(wc -c < "$DIST_DIR/setup.sh" | tr -d ' ') bytes, bootstrap)"
echo "Built: $DIST_DIR/payload.sh ($(wc -c < "$DIST_DIR/payload.sh" | tr -d ' ') bytes, self-extracting)"
echo "Built: $DIST_DIR/probe"
