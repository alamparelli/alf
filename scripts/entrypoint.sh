#!/usr/bin/env bash
set -e

BOOTSTRAP="/home/alf/data/bootstrap.sh"

if [ -f "$BOOTSTRAP" ]; then
    echo "entrypoint: running bootstrap.sh ..."
    chmod +x "$BOOTSTRAP"
    DEBIAN_FRONTEND=noninteractive bash -x "$BOOTSTRAP" 2>&1
    echo "entrypoint: bootstrap.sh done"
fi

exec /opt/alf/alf-daemon "$@"
