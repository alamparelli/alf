#!/usr/bin/env bash
set -e

PACKAGES="/opt/alf/config.d/packages.txt"
RUNTIME="/opt/alf/config.d/runtime.txt"
BOOTSTRAP="/home/alf/data/bootstrap.sh"
STAMP_DIR="/opt/alf/config.d/.stamps"
mkdir -p "$STAMP_DIR"

# Phase 1: Install system packages as root (if packages.txt exists).
# One package name per line. Lines starting with # are ignored.
if [ -f "$PACKAGES" ]; then
    STAMP="$STAMP_DIR/packages"
    if [ ! -f "$STAMP" ] || ! cmp -s "$PACKAGES" "$STAMP"; then
        echo "entrypoint: installing system packages ..."
        if grep -qvE '^\s*#|^\s*$' "$PACKAGES"; then
            DEBIAN_FRONTEND=noninteractive apt-get update -qq \
                && grep -vE '^\s*#|^\s*$' "$PACKAGES" | xargs -r apt-get install -y --no-install-recommends -qq \
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
    STAMP="$STAMP_DIR/runtime"
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

# Phase 1c: Install npm global packages into persistent volume.
NPM_GLOBAL="/opt/alf/config.d/npm-global.txt"
NPM_PREFIX="/opt/alf/user-packages"
if [ -f "$NPM_GLOBAL" ] && command -v npm >/dev/null 2>&1; then
    STAMP="$STAMP_DIR/npm-global"
    if [ ! -f "$STAMP" ] || ! cmp -s "$NPM_GLOBAL" "$STAMP"; then
        echo "entrypoint: installing npm global packages to $NPM_PREFIX ..."
        grep -vE '^\s*#|^\s*$' "$NPM_GLOBAL" | xargs -r npm i -g --prefix "$NPM_PREFIX" --silent \
            || echo "entrypoint: WARNING - npm global install failed, continuing"
        cp "$NPM_GLOBAL" "$STAMP"
    fi
fi

# Phase 2: Run user bootstrap as alf (legacy — deprecated).
# Use services.d/ for persistent processes and bash tool calls for one-time setup.
if [ -f "$BOOTSTRAP" ]; then
    echo "entrypoint: WARNING - bootstrap.sh is deprecated. Use services.d/ for background services."
    echo "entrypoint: running bootstrap.sh (legacy) ..."
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

# Phase 2.6: Lock protected apps (root-owned, read-only for alf user).
# The developer app has marketplace publishing capabilities and must not be
# modifiable by the LLM which runs as uid 1000.
for protected_app in developer; do
    app_dir="/home/alf/data/apps/${protected_app}"
    if [ -d "$app_dir" ]; then
        chown -R root:root "$app_dir"
        find "$app_dir" -type f -exec chmod 444 {} +
        find "$app_dir" -type d -exec chmod 555 {} +
        # data/ must remain writable by alf
        if [ -d "$app_dir/data" ]; then
            chown -R alf:alf "$app_dir/data"
            chmod 755 "$app_dir/data"
        fi
    fi
done

# Phase 3: Drop to alf (uid 1000) and start daemon with zero capabilities.
# setpriv strips all inheritable capabilities - combined with no-new-privileges:true,
# the daemon process cannot regain any capabilities after this point.
# GOMEMLIMIT caps Go heap and makes GC aggressive near the limit.
export GOMEMLIMIT=512MiB
exec setpriv --reuid=1000 --regid=1000 --init-groups --inh-caps=-all /opt/alf/alf-daemon "$@"
