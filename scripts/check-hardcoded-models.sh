#!/usr/bin/env bash
# Guard: fail CI if new hardcoded Claude/OpenAI/etc. model IDs leak into
# production code outside the whitelist. See #194 / #291 for context.
#
# Allowed locations:
#   - router.go                      : alias mapping is the feature
#   - controlcenter/fallback.go      : mirrors the router mapping (no import cycle)
#   - comms/tier.go                  : mirrors the router mapping (no import cycle)
#   - *_test.go                      : tests may pin specific models
#   - **/defaults/**/*.json          : shipped presets, configurable
#   - **/docs/**                     : documentation
#   - web/**/*.html, frontend/**     : UI pickers listing known models
#
# Everything else must resolve models from tier config via
# DefaultFallbackModel (or fail fast).
set -euo pipefail

cd "$(dirname "$0")/.."

PATTERNS='claude-haiku-4-5|claude-sonnet-4-6|claude-opus-4-6|anthropic/claude-'

# shellcheck disable=SC2207
MATCHES=($(grep -rEn "\"($PATTERNS)" \
  --include='*.go' \
  --exclude-dir='vendor' \
  --exclude='*_test.go' \
  internal cmd 2>/dev/null \
  | grep -Ev '(internal/router/router\.go|internal/controlcenter/fallback\.go|internal/comms/tier\.go|internal/controlcenter/types\.go):' \
  || true))

if [ "${#MATCHES[@]}" -gt 0 ]; then
  echo "ERROR: hardcoded model IDs found outside the whitelist:"
  printf '  %s\n' "${MATCHES[@]}"
  echo
  echo "Replace with controlcenter.DefaultFallbackModel(tiers) or"
  echo "comms.DefaultFallbackModel(snap, resolveModel), or fail fast."
  exit 1
fi

echo "OK: no hardcoded model IDs outside whitelist."
