#!/usr/bin/env bash
# Build every WASM capability under wasm-guests/*/ into a .wasm next to
# its manifest.toml. Must run before `go build` because embed.go expects
# the artefacts to exist at compile time.
#
# scripts/dev-deploy.sh invokes this automatically.
set -euo pipefail

cd "$(dirname "$0")"

for dir in */; do
  dir="${dir%/}"
  if [ ! -f "$dir/main.go" ] || [ ! -f "$dir/manifest.toml" ]; then
    continue
  fi
  echo "==> building $dir"
  GOOS=wasip1 GOARCH=wasm go build -o "$dir/$dir.wasm" "./$dir"
  size=$(stat -f%z "$dir/$dir.wasm" 2>/dev/null || stat -c%s "$dir/$dir.wasm")
  echo "    $dir/$dir.wasm ($size bytes)"
done

echo "==> done."
