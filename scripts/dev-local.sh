#!/usr/bin/env bash
set -euo pipefail

# Local development script: build and run ALF locally without CI.
# Usage: ./scripts/dev-local.sh [--clean]

CLEAN=false

for arg in "$@"; do
  case "$arg" in
    --clean) CLEAN=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"

GIT_VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
LOCAL_TAG="${GIT_VERSION}-local"
IMAGE_NAME="alf-local"

if [ "$CLEAN" = true ]; then
  echo "==> Clean: tearing down existing local installation..."
  (cd ~/alf && docker compose down --remove-orphans 2>/dev/null) || true
  rm -rf ~/alf
fi

echo "==> Pruning old ${IMAGE_NAME} images..."
docker images "${IMAGE_NAME}" --format '{{.ID}} {{.Tag}}' | grep -v latest | awk '{print $1}' | xargs -r docker rmi 2>/dev/null || true

echo "==> Building CLI binary..."
go build \
  -ldflags "-s -w -X main.version=${LOCAL_TAG}" \
  -o /tmp/alf-local \
  ./cmd/alf/

echo "==> Building Docker image: ${IMAGE_NAME}:${LOCAL_TAG}..."
docker build --build-arg BUILD_VERSION="${GIT_VERSION}" -t "${IMAGE_NAME}:${LOCAL_TAG}" -t "${IMAGE_NAME}:latest" .

echo "==> Running alf init with local image..."
export ALF_IMAGE="${IMAGE_NAME}:latest"
/tmp/alf-local init

echo "==> Done. ALF running with local image ${IMAGE_NAME}:${LOCAL_TAG}"
