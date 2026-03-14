#!/usr/bin/env bash
set -e

PACKAGES="/opt/alf/config.d/packages.txt"
RUNTIME="/opt/alf/config.d/runtime.txt"
BOOTSTRAP="/home/alf/data/bootstrap.sh"

# Phase 1: Install system packages as root (if packages.txt exists).
# One package name per line. Lines starting with # are ignored.
if [ -f "$PACKAGES" ]; then
    STAMP="/tmp/.packages-stamp"
    if [ ! -f "$STAMP" ] || ! cmp -s "$PACKAGES" "$STAMP"; then
        echo "entrypoint: installing system packages ..."
        PKGS=$(grep -v '^\s*#' "$PACKAGES" | grep -v '^\s*$' | tr '\n' ' ')
        if [ -n "$PKGS" ]; then
            DEBIAN_FRONTEND=noninteractive apt-get update -qq \
                && apt-get install -y --no-install-recommends -qq $PKGS \
                && rm -rf /var/lib/apt/lists/* \
                || echo "entrypoint: WARNING - package install failed, continuing"
        fi
        cp "$PACKAGES" "$STAMP"
    fi
fi

# Phase 1b: Install JS runtime (if runtime.txt exists).
# Supports: node, deno, bun. Installs once (stamp check).
if [ -f "$RUNTIME" ]; then
    RT=$(grep -v '^\s*#' "$RUNTIME" | grep -v '^\s*$' | head -1 | tr -d '[:space:]')
    STAMP="/tmp/.runtime-stamp"
    PREV=$(cat "$STAMP" 2>/dev/null || echo "")
    if [ -n "$RT" ] && [ "$RT" != "$PREV" ]; then
        echo "entrypoint: installing JS runtime: $RT ..."
        case "$RT" in
            node)
                DEBIAN_FRONTEND=noninteractive apt-get update -qq \
                    && apt-get install -y --no-install-recommends -qq nodejs npm \
                    && rm -rf /var/lib/apt/lists/* \
                    || echo "entrypoint: WARNING - node install failed"
                ;;
            deno)
                curl -fsSL https://deno.land/install.sh | DENO_INSTALL=/usr/local sh \
                    || echo "entrypoint: WARNING - deno install failed"
                ;;
            bun)
                curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local bash \
                    || echo "entrypoint: WARNING - bun install failed"
                ;;
            *)
                echo "entrypoint: WARNING - unknown JS runtime: $RT"
                ;;
        esac
        echo "$RT" > "$STAMP"
    fi
fi

# Phase 2: Run user bootstrap as alf (pip install, start services, etc.).
# Never runs as root - no apt, no writes to /usr, /etc, /root.
if [ -f "$BOOTSTRAP" ]; then
    echo "entrypoint: running bootstrap.sh ..."
    chmod +x "$BOOTSTRAP"
    chown alf:alf "$BOOTSTRAP"
    DEBIAN_FRONTEND=noninteractive su -s /bin/bash alf -c "bash -x $BOOTSTRAP" 2>&1 || \
        echo "entrypoint: WARNING - bootstrap.sh exited with code $?, continuing anyway"
    echo "entrypoint: bootstrap.sh done"
fi

# Phase 2.5: Fix all permissions as root (before dropping privileges).
# The daemon runs as uid 1000 and cannot chown, so we do it here.
chown -R alf:alf /home/alf
chown -R alf:alf /home/alf/data
chmod -R g+ws /home/alf/data
chown -R alf:alf /opt/alf/config.d /opt/alf/vault-data
chown alf:alf /etc/resolv.conf 2>/dev/null || true

# Phase 3: Drop to alf (uid 1000) and start daemon with zero capabilities.
# setpriv strips all inheritable capabilities — combined with no-new-privileges:true,
# the daemon process cannot regain any capabilities after this point.
exec setpriv --reuid=1000 --regid=1000 --init-groups --inh-caps=-all /opt/alf/alf-daemon "$@"
