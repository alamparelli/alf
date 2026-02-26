#!/bin/sh
set -e

# Auto-increment patch version, tag, and push.
# Usage: ./scripts/release.sh

# Get latest tag
latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

# Parse semver
major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
minor=$(echo "$latest" | sed 's/v//' | cut -d. -f2)
patch=$(echo "$latest" | sed 's/v//' | cut -d. -f3)

# Increment patch
patch=$((patch + 1))
next="v${major}.${minor}.${patch}"

echo "Current: ${latest}"
echo "Next:    ${next}"
echo ""

# Tag and push everything in one go
branch=$(git branch --show-current)
echo "Tagging ${next} and pushing ${branch}..."
git tag "$next"
git push origin "$branch" --follow-tags

echo ""
echo "Released ${next}"
