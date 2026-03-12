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
  # Local build: push branch only, skip tag push to avoid triggering CI/CD.
  echo "Tagging ${next} (local build — tag not pushed to remote)..."
  git push origin "$branch"
else
  # CI/CD build: push branch + tag to trigger GitHub Actions workflow.
  echo "Tagging ${next} and pushing ${branch}..."
  git push origin "$branch" "$next"
fi

echo ""
echo "Released ${next}"

if [ "$LOCAL_BUILD" = true ]; then
  echo ""
  echo "Vendoring vault-proxy source..."
  rm -rf third_party/vault-proxy
  mkdir -p third_party/vault-proxy
  rsync -a --exclude .git --exclude vault-data --exclude '/vault-server' --exclude '/vault-cli' \
    ../Projects/vault-proxy/ third_party/vault-proxy/

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
  echo ""
  echo "Pushed ${REGISTRY}:${version} + :latest"
fi
