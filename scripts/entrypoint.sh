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
# alf (uid 1000) = LLM subprocess, alfd (uid 1001) = daemon.
# Clean up stale Unix sockets from previous runs (chown on sockets fails on Docker Desktop).
find /home/alf/data -name '*.sock' -type s -delete 2>/dev/null || true
chown -R alf:alf /home/alf               # subprocess HOME
chown -R alf:alf /home/alf/data          # workspace (both users via group)
chmod -R g+ws /home/alf/data
chown -R alfd:alf /opt/alf/config.d      # daemon owns config, subprocess reads via group
chmod 750 /opt/alf/config.d
find /opt/alf/config.d -type d -exec chmod 750 {} +   # subdirs also 750 (no LLM write)
chown -R alfd:alfd /opt/alf/vault-data   # daemon-only: group alfd, LLM cannot read
chmod 700 /opt/alf/vault-data
# Pre-create daemon directories (daemon runs as alfd but data/ is alf-owned volume).
for d in logs logs/events sessions config tools skills context agents agents/teams apps documents; do
    mkdir -p "/home/alf/data/$d"
done
chown -R alfd:alf /home/alf/data/logs /home/alf/data/sessions /home/alf/data/config /home/alf/data/context /home/alf/data/documents
chmod -R 750 /home/alf/data/logs /home/alf/data/sessions
chown -R alf:alf /home/alf/data/agents /home/alf/data/apps /home/alf/data/tools /home/alf/data/skills
chmod -R g+ws /home/alf/data/agents /home/alf/data/apps
# tools.d/ owned by daemon: LLM gets r-x via group, no write (anti-shadow CWE-94).
mkdir -p /home/alf/data/tools.d
chown alfd:alf /home/alf/data/tools.d
chmod 755 /home/alf/data/tools.d
chown alf:alf /etc/resolv.conf 2>/dev/null || true

# Allow subprocess (alf) to install packages (pip, npm).
for d in /home/alf/.local /home/alf/.npm /home/alf/.cache; do
    mkdir -p "$d"
    chown -R alf:alf "$d"
    chmod -R g+rwX "$d"
done
chown -R alf:alf /opt/alf/user-packages
chmod -R g+ws /opt/alf/user-packages

# Ensure subprocess can read its own .claude/ auth.
chmod -R g+rX /home/alf/.claude 2>/dev/null || true

# Ensure Codex CLI cache dir is owned by alf (volume mount may create as root).
chown -R alf:alf /home/alf/.codex 2>/dev/null || true

# Docker secrets: make them readable by daemon (alfd/uid 1001).
# Docker Compose standalone mounts secrets as read-only tmpfs (chown impossible).
# Copy to a writable location owned by alfd, then re-export *_FILE env vars.
if [ -d /run/secrets ]; then
    mkdir -p /run/alf-secrets
    cp /run/secrets/* /run/alf-secrets/ 2>/dev/null || true
    chown -R alfd:alfd /run/alf-secrets
    chmod 0400 /run/alf-secrets/*
    # Re-point *_FILE env vars to the copied secrets.
    for f in /run/alf-secrets/*; do
        name=$(basename "$f")
        upper=$(echo "$name" | tr '[:lower:]' '[:upper:]')
        export "${upper}_FILE=/run/alf-secrets/${name}"
    done

    # Vault master password: migrate from Docker secret to vault-data (alfd-only).
    # This is a one-time bridge — future installs use CC setup or alf init.
    if [ -f /run/secrets/vault_master_password ]; then
        pw=$(cat /run/secrets/vault_master_password)
        if [ -n "$pw" ]; then
            pwfile="/opt/alf/vault-data/.master-password"
            if [ ! -f "$pwfile" ]; then
                echo "$pw" > "$pwfile"
                chown alfd:alfd "$pwfile"
                chmod 0400 "$pwfile"
                echo "entrypoint: migrated vault master password to vault-data"
            fi
        fi
        # Unset so daemon doesn't read from Docker secret path.
        unset VAULT_MASTER_PASSWORD_FILE
    fi

    # CC auth token: migrate from Docker secret to vault-data (alfd-only).
    if [ -f /run/secrets/cc_auth_token ]; then
        tok=$(cat /run/secrets/cc_auth_token)
        if [ -n "$tok" ]; then
            tokfile="/opt/alf/vault-data/.cc_auth_token"
            echo "$tok" > "$tokfile"
            chown alfd:alfd "$tokfile"
            chmod 0400 "$tokfile"
        fi
    fi
fi

# Phase 2.6: Seed default apps from bundled defaults (as root, before locking).
# This ensures bundled app updates are applied even when files are root-locked.
DEFAULTS_DIR="/opt/alf/defaults/apps"
APPS_DIR="/home/alf/data/apps"
if [ -d "$DEFAULTS_DIR" ]; then
    for app_src in "$DEFAULTS_DIR"/*/; do
        slug=$(basename "$app_src")
        app_dest="$APPS_DIR/$slug"
        mkdir -p "$app_dest"
        # Copy all files except data/ (preserve user data)
        rsync -a --exclude='data/' "$app_src" "$app_dest/" 2>/dev/null || \
            cp -r "$app_src"* "$app_dest/" 2>/dev/null || true
        chown -R alf:alf "$app_dest"
        echo "entrypoint: seeded app $slug"
    done
fi

# Phase 2.7: Lock protected apps (root-owned, read-only for alf user).
# The developer app has marketplace publishing capabilities and must not be
# modifiable by the LLM which runs as uid 1000. Runs AFTER seeding.
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

# Phase 2.9: Start nettrack helper (runs as root with CAP_NET_ADMIN for conntrack).
# The helper writes connection events to a Unix socket that the daemon reads.
if [ -x /opt/alf/bin/nettrack-helper ]; then
    /opt/alf/bin/nettrack-helper &
    echo "entrypoint: nettrack-helper started (pid=$!)"
fi

# Phase 2.95: Block vault TCP port for app processes (defense-in-depth).
# vault-server uses Unix socket now; this blocks misconfigured fallback.
if command -v iptables >/dev/null 2>&1; then
    iptables -A OUTPUT -p tcp --dport 8390 -m owner --uid-owner 1000 -j REJECT 2>/dev/null || true
    echo "entrypoint: blocked uid 1000 -> tcp/8390"
fi

# Phase 3: Drop to alfd (uid 1001, gid 1001) and start daemon.
# Keep CAP_SETUID+CAP_SETGID so the daemon can spawn subprocesses as alf (uid 1000).
# --init-groups loads supplementary groups from /etc/group (alfd is also in group alf).
# GOMEMLIMIT caps Go heap and makes GC aggressive near the limit.
export GOMEMLIMIT=512MiB
exec setpriv --reuid=1001 --regid=1001 --init-groups \
    --inh-caps=-all,+setuid,+setgid,+sys_admin,+sys_chroot,+chown \
    --ambient-caps=+setuid,+setgid,+sys_admin,+sys_chroot,+chown \
    /opt/alf/alf-daemon "$@"
