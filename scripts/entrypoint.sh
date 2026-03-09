#!/usr/bin/env bash
set -e

PACKAGES="/opt/alf/config.d/packages.txt"
BOOTSTRAP="/home/alf/data/bootstrap.sh"

# Phase 1: Install system packages as root (if packages.txt exists).
# One package name per line. Runs only when the file changes (stamp check).
if [ -f "$PACKAGES" ]; then
    STAMP="/tmp/.packages-stamp"
    if [ ! -f "$STAMP" ] || ! cmp -s "$PACKAGES" "$STAMP"; then
        echo "entrypoint: installing system packages ..."
        DEBIAN_FRONTEND=noninteractive apt-get update -qq \
            && xargs -a "$PACKAGES" apt-get install -y --no-install-recommends -qq \
            && rm -rf /var/lib/apt/lists/* \
            || echo "entrypoint: WARNING — package install failed, continuing"
        cp "$PACKAGES" "$STAMP"
    fi
fi

# Phase 2: Run user bootstrap as alf (pip install, start services, etc.).
# Never runs as root — no apt, no writes to /usr, /etc, /root.
if [ -f "$BOOTSTRAP" ]; then
    echo "entrypoint: running bootstrap.sh ..."
    chmod +x "$BOOTSTRAP"
    chown alf:alf "$BOOTSTRAP"
    DEBIAN_FRONTEND=noninteractive su -s /bin/bash alf -c "bash -x $BOOTSTRAP" 2>&1 || \
        echo "entrypoint: WARNING — bootstrap.sh exited with code $?, continuing anyway"
    echo "entrypoint: bootstrap.sh done"
fi

exec /opt/alf/alf-daemon "$@"
