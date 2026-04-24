#!/usr/bin/env bash
# End-to-end interop proof for ticket #387.
#
# 1. Build the POC
# 2. Generate a fresh Ed25519 keypair (our code)
# 3. Sign a sample payload (our code)
# 4. Self-verify via our code (Go round-trip)
# 5. Cross-verify via the stock `minisign` CLI (interop proof)
#
# If step 5 passes, the POC thesis — "Go stdlib Ed25519 + minisign-compatible
# format gives us the operator escape hatch" — is validated. Any operator
# with `minisign` installed can verify an alf-signed bundle without alf
# tooling.

set -euo pipefail

cd "$(dirname "$0")/.."

# Clean slate
rm -f alf-poc.pub alf-poc.sec.raw testdata/sample.bin.minisig
echo "--- sample payload (512 bytes of 'alf-0.8.0-trust-poc-...')" > testdata/sample.bin
dd if=/dev/urandom of=testdata/sample.bin bs=512 count=1 status=none

# Build (stays in the POC dir — does not pollute repo root)
go build -o trust-poc ./

echo
echo "=== 1. keygen ==="
./trust-poc keygen -pub alf-poc.pub -sec alf-poc.sec.raw
echo
echo "--- pubkey file ---"
cat alf-poc.pub

echo
echo "=== 2. sign ==="
./trust-poc sign -sec alf-poc.sec.raw -m testdata/sample.bin -c "alf-0.8.0 interop POC"
echo
echo "--- signature file ---"
cat testdata/sample.bin.minisig

echo
echo "=== 3. verify (our Go code) ==="
./trust-poc verify -pub alf-poc.pub -m testdata/sample.bin

echo
echo "=== 4. cross-verify (stock minisign CLI) ==="
minisign -V -p alf-poc.pub -m testdata/sample.bin

echo
echo "=== 5. tamper detection ==="
echo
echo "--- tamper with the payload ---"
echo "tampered" >> testdata/sample.bin
set +e
./trust-poc verify -pub alf-poc.pub -m testdata/sample.bin
echo "  → our code rejected tampered payload (exit $?)"
minisign -V -p alf-poc.pub -m testdata/sample.bin
echo "  → minisign CLI rejected tampered payload (exit $?)"
set -e

echo
echo "All interop checks passed — POC thesis validated."
