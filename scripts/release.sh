#!/bin/sh
set -e

# Auto-increment patch version, tag, and push.
# Usage: ./scripts/release.sh [--local]
#   --local  Build and push Docker image locally instead of waiting for CI/CD

LOCAL_BUILD=false
for arg in "$@"; do
  case "$arg" in
    --local) LOCAL_BUILD=true ;;
  esac
done

REGISTRY="ghcr.io/alamparelli/alf"

# Get latest tag
latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

# Parse semver
major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
minor=$(echo "$latest" | sed 's/v//' | cut -d. -f2)
patch=$(echo "$latest" | sed 's/v//' | cut -d. -f3)

# Increment patch
patch=$((patch + 1))
next="v${major}.${minor}.${patch}"
version="${major}.${minor}.${patch}"

echo "Current: ${latest}"
echo "Next:    ${next}"
echo ""

# Tag and push
branch=$(git branch --show-current)
git tag "$next"

if [ "$LOCAL_BUILD" = true ]; then
  # Local build: push code only (no tag push = no CI/CD trigger).
  echo "Pushing ${branch} (tag ${next} kept local - no CI/CD)..."
  git push origin "$branch"
else
  # CI/CD: push code + tag to trigger pipeline.
  echo "Tagging ${next} and pushing ${branch}..."
  git push origin "$branch" "$next"
fi

echo ""
echo "Released ${next}"

if [ "$LOCAL_BUILD" = true ]; then
  WHISPER_REGISTRY="ghcr.io/alamparelli/whisper-service"
  EMBED_REGISTRY="ghcr.io/alamparelli/embed-service"

  echo ""
  # Vendor vault-proxy — prefer local dev, fallback to NFS
  VAULT_PROXY_SRC="${VAULT_PROXY_SRC:-$HOME/Dev/Projects/vault-proxy}"
  if [ ! -d "${VAULT_PROXY_SRC}" ]; then
    VAULT_PROXY_SRC="/Volumes/ALF_NFS/repos/vault-proxy"
  fi
  test -d "${VAULT_PROXY_SRC}" || { echo "ERROR: vault-proxy not found (tried ~/Dev/Projects and NFS)"; exit 1; }
  echo "Vendoring vault-proxy from ${VAULT_PROXY_SRC}..."
  rm -rf third_party/vault-proxy
  mkdir -p third_party/vault-proxy
  rsync -a --exclude .git --exclude vault-data --exclude '/vault-server' --exclude '/vault-cli' \
    "${VAULT_PROXY_SRC}/" third_party/vault-proxy/

  echo "Building Docker image locally (linux/amd64 + linux/arm64)..."
  # Ensure a multi-platform builder exists.
  if ! docker buildx inspect multiarch >/dev/null 2>&1; then
    docker buildx create --name multiarch
  fi
  docker buildx build \
    --builder multiarch \
    --platform linux/amd64,linux/arm64 \
    --push \
    -t "${REGISTRY}:${version}" \
    -t "${REGISTRY}:latest" \
    .

  rm -rf third_party/vault-proxy

  # Rebuild whisper-service if files changed OR image doesn't exist in registry yet.
  WHISPER_EXISTS=$(docker manifest inspect "${WHISPER_REGISTRY}:latest" >/dev/null 2>&1 && echo yes || echo no)
  if [ "$WHISPER_EXISTS" = "no" ] || git diff --name-only "${latest}" HEAD -- whisper-service/ | grep -q .; then
    echo "Building whisper-service Docker image (linux/amd64 + linux/arm64)..."
    docker buildx build \
      --builder multiarch \
      --platform linux/amd64,linux/arm64 \
      --push \
      -t "${WHISPER_REGISTRY}:${version}" \
      -t "${WHISPER_REGISTRY}:latest" \
      ./whisper-service
    echo "Pushed ${WHISPER_REGISTRY}:${version} + :latest"
  else
    echo "whisper-service unchanged since ${latest} — skipping build"
  fi

  # Rebuild embed-service if files changed OR image doesn't exist in registry yet.
  EMBED_EXISTS=$(docker manifest inspect "${EMBED_REGISTRY}:latest" >/dev/null 2>&1 && echo yes || echo no)
  if [ "$EMBED_EXISTS" = "no" ] || git diff --name-only "${latest}" HEAD -- embed-service/ cmd/embed-server/ internal/memstore/ | grep -q .; then
    echo "Building embed-service Docker image (linux/amd64 + linux/arm64)..."
    docker buildx build \
      --builder multiarch \
      --platform linux/amd64,linux/arm64 \
      --push \
      -t "${EMBED_REGISTRY}:${version}" \
      -t "${EMBED_REGISTRY}:latest" \
      -f embed-service/Dockerfile \
      .
    echo "Pushed ${EMBED_REGISTRY}:${version} + :latest"
  else
    echo "embed-service unchanged since ${latest} — skipping build"
  fi

  echo ""
  echo "Pushed ${REGISTRY}:${version} + :latest"

  # Build and upload alpha CLI binaries.
  ALPHA_SCRIPT="$(cd "$(dirname "$0")" && pwd)/alpha-deploy.sh"
  if [ -f "$ALPHA_SCRIPT" ]; then
    echo ""
    echo "Building alpha CLI binaries..."
    "$ALPHA_SCRIPT" --binaries-only
  fi
fi
