# Trust-model POC — minisign-compatible Ed25519 in Go stdlib

Proof-of-concept for ticket [#387](https://github.com/alamparelli/alf/issues/387).

## Thesis

The alf daemon must be able to sign and verify capability bundles using only the Go standard library (`crypto/ed25519` + `golang.org/x/crypto/blake2b`), while producing files that the stock [`minisign`](https://jedisct1.github.io/minisign/) CLI can verify out-of-band. This gives operators a reliable escape-hatch: they can validate an alf-signed bundle with no alf tooling on the path — only `minisign`, which is packaged by Homebrew, apt, and most distros.

Validated on this repo on 2026-04-24 against `minisign 0.12`.

## What this POC proves

1. Our keygen produces a public key that `minisign -V` accepts.
2. Our signer produces a `.minisig` file that `minisign -V` validates as authentic.
3. Our verifier rejects tampered payloads with the same fail-closed behaviour as `minisign -V`.

## What this POC is not

- Not a production-ready signing tool. The secret key is stored raw on disk here; production stores it in vault user-scope (ticket [#395](https://github.com/alamparelli/alf/issues/395)).
- Not a CLI spec. `alf sign` and `alf verify` are defined in the trust-model section of `docs/ARCHITECTURE-SECURITY.md` §7 and implemented under [#388](https://github.com/alamparelli/alf/issues/388).
- Not the envelope layer. Ticket [#397](https://github.com/alamparelli/alf/issues/397) specifies the canonical envelope that wraps the payload before signing — this POC signs a raw byte blob directly.

## Running it

```sh
# One-shot interop test
bash testdata/run.sh
```

Expected tail:

```
=== 4. cross-verify (stock minisign CLI) ===
Signature and comment signature verified
Trusted comment: alf-0.8.0 interop POC
...
All interop checks passed — POC thesis validated.
```

## File format reference

The POC produces files that conform to minisign 0.9+ for the `ED` (pre-hashed Ed25519) algorithm.

**Public key** (`alf-poc.pub`):

```
untrusted comment: <arbitrary description>
<base64(2-byte algo || 8-byte key ID || 32-byte Ed25519 public key)>
```

**Signature** (`<file>.minisig`):

```
untrusted comment: <arbitrary description>
<base64(2-byte algo || 8-byte key ID || 64-byte Ed25519 signature over BLAKE2b-512(payload))>
trusted comment: <string, covered by global signature>
<base64(64-byte Ed25519 signature over (raw_signature || trusted_comment_bytes))>
```

Algorithms:

- `Ed` (0x45 0x64) — legacy, signs raw bytes. Accepted in pubkey files; never emitted.
- `ED` (0x45 0x44) — pre-hashed, signs BLAKE2b-512 of the payload. Always used for signing.

## Why pre-hashed, not legacy

Legacy Ed25519 requires the verifier to stream the full payload through the signature function. For multi-MB WASM bundles that is both slow and memory-hostile. Pre-hashed mode hashes the payload once with BLAKE2b-512 and signs the 64-byte digest — verification is `O(sig_size)` regardless of payload size, and verifiers can stream the hash computation without buffering the payload.
