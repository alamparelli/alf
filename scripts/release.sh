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

  echo ""
  echo "Vendoring vault-proxy source..."
  test -d /Volumes/ALF_NFS/repos/vault-proxy || { echo "ERROR: NFS share not mounted (vault-proxy not found)"; exit 1; }
  rm -rf third_party/vault-proxy
  mkdir -p third_party/vault-proxy
  rsync -a --exclude .git --exclude vault-data --exclude '/vault-server' --exclude '/vault-cli' \
    /Volumes/ALF_NFS/repos/vault-proxy/ third_party/vault-proxy/

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

  # Only rebuild whisper-service if its files changed since last tag.
  if git diff --name-only "${latest}" HEAD -- whisper-service/ | grep -q .; then
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
