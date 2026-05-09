package envelope

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Minisign-compatible signature primitives, extracted from the #387 POC
// at technical/poc/trust-minisign-compat/. See that branch's README for
// the interop test against stock `minisign -V`.
//
// Production responsibilities added here (over the POC):
//   - Typed public/secret key wrappers that make key IDs first-class
//   - Parsers for the minisign file formats so the daemon reads and
//     emits exactly what stock minisign produces
//   - No CLI, no os.ReadFile — inputs are []byte, outputs are []byte
//     (TOCTOU-safe per #388 deliverable 3)

// Algo prefixes: first two bytes of the on-wire pubkey / signature blob.
var (
	algoPrehashed = [2]byte{'E', 'D'} // Ed25519 over BLAKE2b-512 hash — our only sign path
	algoPlain     = [2]byte{'E', 'd'} // Ed25519 over raw bytes — legacy pubkey format, read-only
)

// KeyID is the 8-byte minisign key identifier. Every signature names
// its signer via this ID; the trust store is keyed on it.
type KeyID [8]byte

// Hex returns the uppercase hex representation (16 chars), matching
// minisign's `signer_key_fingerprint` field and the ARCHITECTURE-SECURITY.md
// §7.10.3 envelope structure.
func (k KeyID) Hex() string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 16)
	for i, b := range k {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

// HexLower returns the lowercase hex representation (16 chars).
// Used as the publisher fingerprint for #392 namespace-scoped handle
// references — the manifest schema (`dependsHandlePattern` in
// envelope/schema.go) requires lowercase, so capability provider
// installs persist their KeyID through this form. The full 16 chars
// are kept (rather than truncating per the §H2 "first N hex chars"
// note in the spec) because:
//   - 16 hex chars = 64 bits = ~280 trillion combinations; collision
//     risk is negligible even with a billion providers
//   - Once references like `<short>:bluetooth.scan` ship in any
//     manifest, changing the truncation length is a breaking schema
//     change. Picking "no truncation" once means N is documented
//     and stable.
func (k KeyID) HexLower() string {
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i, b := range k {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

// ErrKeyIDInvalidHex is returned by KeyIDFromHex when the input is not
// a 16-char hex string. Used to round-trip
// `[[depends]].handle = "<ns>:<id>"` namespace strings back into the
// trust-store KeyID type for revocation cascade lookups (#392 Stage 5).
var ErrKeyIDInvalidHex = errors.New("envelope: KeyID hex string must be 16 chars of [0-9a-fA-F]")

// KeyIDFromHex parses the 16-char hex form of a KeyID (as produced by
// Hex / HexLower) back into the typed [8]byte value. Accepts both
// uppercase and lowercase. Used by the runtime revocation cascade to
// turn a manifest's `[[depends]].handle = "<ns>:<id>"` namespace into
// the KeyID needed to track which provider key the consumer depends
// on. Returns ErrKeyIDInvalidHex on length mismatch or non-hex chars.
//
// Reuses the package-private hexNibble helper from crl.go (which
// returns an error on non-hex bytes — same shape, different error
// wrap — so callers see ErrKeyIDInvalidHex consistently).
func KeyIDFromHex(s string) (KeyID, error) {
	if len(s) != 16 {
		return KeyID{}, fmt.Errorf("%w: got len=%d", ErrKeyIDInvalidHex, len(s))
	}
	var k KeyID
	for i := 0; i < 8; i++ {
		hi, hiErr := hexNibble(s[i*2])
		lo, loErr := hexNibble(s[i*2+1])
		if hiErr != nil || loErr != nil {
			return KeyID{}, fmt.Errorf("%w: non-hex char at index %d", ErrKeyIDInvalidHex, i*2)
		}
		k[i] = (hi << 4) | lo
	}
	return k, nil
}

// PublicKey wraps an Ed25519 public key with its minisign key ID.
type PublicKey struct {
	ID  KeyID
	Key ed25519.PublicKey
}

// PrivateKey wraps an Ed25519 private key with its minisign key ID.
// The ID must match the matching PublicKey's ID — Sign() copies it into
// the signature blob so the verifier can look up the key before doing
// any cryptographic work.
type PrivateKey struct {
	ID  KeyID
	Key ed25519.PrivateKey
}

// GenerateKey produces a fresh Ed25519 keypair with a random 8-byte
// key ID. Used by the daemon's first-boot init (#387) to mint the
// local daemon key. Third-party keys are imported via ParsePublicKey,
// never generated here.
func GenerateKey() (PublicKey, PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PublicKey{}, PrivateKey{}, fmt.Errorf("envelope: generate Ed25519 keypair: %w", err)
	}
	var id KeyID
	if _, err := rand.Read(id[:]); err != nil {
		return PublicKey{}, PrivateKey{}, fmt.Errorf("envelope: generate key ID: %w", err)
	}
	return PublicKey{ID: id, Key: pub}, PrivateKey{ID: id, Key: priv}, nil
}

// Sign produces a minisign-compatible signature blob over data. The
// blob is 74 bytes: 2-byte algorithm prefix + 8-byte key ID +
// 64-byte Ed25519 signature. Data is BLAKE2b-512 pre-hashed — the
// only path we produce (algoPrehashed).
//
// Returned bytes are the on-wire signature. To emit a .minisig file,
// pass through EncodeSignatureFile.
func Sign(priv PrivateKey, data []byte) ([]byte, error) {
	if len(priv.Key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("envelope: private key wrong size: got %d, want %d", len(priv.Key), ed25519.PrivateKeySize)
	}
	hash := blake2b.Sum512(data)
	rawSig := ed25519.Sign(priv.Key, hash[:])

	out := make([]byte, 0, 2+8+ed25519.SignatureSize)
	out = append(out, algoPrehashed[:]...)
	out = append(out, priv.ID[:]...)
	out = append(out, rawSig...)
	return out, nil
}

// VerifySignature decodes sig as a minisign signature blob, checks
// that its key ID matches pub, that the algorithm is the supported
// prehashed variant, and that the Ed25519 signature validates against
// BLAKE2b-512(data).
//
// Errors returned are drawn from the typed set below so callers can
// map to the §7.10.7 test vector outcomes via errors.Is.
//
// The high-level pipeline entry point is envelope.Verify (verify.go);
// this function is the raw cryptographic primitive it delegates to.
func VerifySignature(pub PublicKey, data, sig []byte) error {
	if len(pub.Key) != ed25519.PublicKeySize {
		return fmt.Errorf("envelope: public key wrong size: got %d, want %d", len(pub.Key), ed25519.PublicKeySize)
	}
	if len(sig) != 2+8+ed25519.SignatureSize {
		return fmt.Errorf("%w: got %d bytes", ErrSignatureMalformed, len(sig))
	}
	var algo [2]byte
	copy(algo[:], sig[0:2])
	if algo != algoPrehashed {
		return fmt.Errorf("%w: got %q (only %q accepted)", ErrAlgorithmUnsupported, string(algo[:]), string(algoPrehashed[:]))
	}
	var keyID KeyID
	copy(keyID[:], sig[2:10])
	if keyID != pub.ID {
		return fmt.Errorf("%w: sig=%s pub=%s", ErrKeyIDMismatch, keyID.Hex(), pub.ID.Hex())
	}

	hash := blake2b.Sum512(data)
	if !ed25519.Verify(pub.Key, hash[:], sig[10:]) {
		return ErrSignatureInvalid
	}
	return nil
}

// Typed errors for the verify path. Stable surface for §7.10.7 test
// vectors.
var (
	ErrAlgorithmUnsupported = errors.New("envelope: algorithm unsupported")
	ErrKeyIDMismatch        = errors.New("envelope: key ID does not match public key")
	ErrSignatureMalformed   = errors.New("envelope: signature blob malformed")
	ErrSignatureInvalid     = errors.New("envelope: signature does not match data")
	ErrPubkeyMalformed      = errors.New("envelope: pubkey file malformed")
	ErrSigFileMalformed     = errors.New("envelope: signature file malformed")
)

// -----------------------------------------------------------------------------
// Minisign file format — text wrappers around the raw blobs.
//
// Pubkey file (2 lines):
//   untrusted comment: <string>
//   <base64(2-byte algo || 8-byte key ID || 32-byte pubkey)>
//
// Signature file (4 lines):
//   untrusted comment: <string>
//   <base64(2-byte algo || 8-byte key ID || 64-byte signature)>
//   trusted comment: <string>
//   <base64(64-byte global signature over signature||trusted-comment)>
//
// We decode both; we emit our own with fixed untrusted/trusted comments
// driven by the caller's context (bundle ID / signer fingerprint).
// -----------------------------------------------------------------------------

// EncodePublicKeyFile serialises pub as a minisign-compatible public key
// file, ready to write to disk or embed in a trust store entry.
func EncodePublicKeyFile(pub PublicKey, untrustedComment string) ([]byte, error) {
	if len(pub.Key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("envelope: pubkey wrong size: %d", len(pub.Key))
	}
	buf := make([]byte, 0, 2+8+ed25519.PublicKeySize)
	buf = append(buf, algoPlain[:]...)
	buf = append(buf, pub.ID[:]...)
	buf = append(buf, pub.Key...)
	text := "untrusted comment: " + untrustedComment + "\n" +
		base64.StdEncoding.EncodeToString(buf) + "\n"
	return []byte(text), nil
}

// ParsePublicKeyFile decodes a minisign public key file into a typed
// PublicKey. Accepts both the plain ("Ed") and prehashed ("ED") algo
// prefixes — minisign's own keys carry plain, our own emit prehashed is
// optional. Either way, what counts for sig verification is the signature
// blob's own algo byte, not the pubkey file's.
func ParsePublicKeyFile(raw []byte) (PublicKey, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return PublicKey{}, fmt.Errorf("%w: expected 2 lines, got %d", ErrPubkeyMalformed, len(lines))
	}
	decoded, err := base64.StdEncoding.DecodeString(lines[len(lines)-1])
	if err != nil {
		return PublicKey{}, fmt.Errorf("%w: base64: %w", ErrPubkeyMalformed, err)
	}
	if len(decoded) != 2+8+ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("%w: payload size %d", ErrPubkeyMalformed, len(decoded))
	}
	var algo [2]byte
	copy(algo[:], decoded[0:2])
	if algo != algoPlain && algo != algoPrehashed {
		return PublicKey{}, fmt.Errorf("%w: algo %q", ErrPubkeyMalformed, string(algo[:]))
	}
	var id KeyID
	copy(id[:], decoded[2:10])
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pub, decoded[10:])
	return PublicKey{ID: id, Key: pub}, nil
}

// EncodeSignatureFile wraps a raw signature blob (as produced by Sign)
// into the 4-line minisign signature file format. The trusted comment
// is covered by a second (global) signature so it cannot be tampered
// with post-signing.
func EncodeSignatureFile(priv PrivateKey, sigBlob []byte, trustedComment string) ([]byte, error) {
	if len(sigBlob) != 2+8+ed25519.SignatureSize {
		return nil, fmt.Errorf("envelope: sig blob size %d, want %d", len(sigBlob), 2+8+ed25519.SignatureSize)
	}
	rawSig := sigBlob[10:]

	// Global signature: Ed25519 over (raw_sig || trusted_comment).
	globalInput := make([]byte, 0, len(rawSig)+len(trustedComment))
	globalInput = append(globalInput, rawSig...)
	globalInput = append(globalInput, []byte(trustedComment)...)
	globalSig := ed25519.Sign(priv.Key, globalInput)

	text := "untrusted comment: signature from alf key " + priv.ID.Hex() + "\n" +
		base64.StdEncoding.EncodeToString(sigBlob) + "\n" +
		"trusted comment: " + trustedComment + "\n" +
		base64.StdEncoding.EncodeToString(globalSig) + "\n"
	return []byte(text), nil
}

// ParseSignatureFile returns the signature blob, the trusted-comment
// string, and the global signature over (rawSig || trustedComment).
// The caller verifies both the main signature (via Verify) and the
// global signature (via VerifyGlobalComment).
func ParseSignatureFile(raw []byte) (sigBlob []byte, trustedComment string, globalSig []byte, _ error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 4 {
		return nil, "", nil, fmt.Errorf("%w: expected 4 lines, got %d", ErrSigFileMalformed, len(lines))
	}
	blob, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		return nil, "", nil, fmt.Errorf("%w: decode sig blob: %w", ErrSigFileMalformed, err)
	}
	if len(blob) != 2+8+ed25519.SignatureSize {
		return nil, "", nil, fmt.Errorf("%w: sig blob size %d", ErrSigFileMalformed, len(blob))
	}
	const trustedPrefix = "trusted comment: "
	if !strings.HasPrefix(lines[2], trustedPrefix) {
		return nil, "", nil, fmt.Errorf("%w: missing 'trusted comment:' line", ErrSigFileMalformed)
	}
	trusted := strings.TrimPrefix(lines[2], trustedPrefix)
	gSig, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil {
		return nil, "", nil, fmt.Errorf("%w: decode global sig: %w", ErrSigFileMalformed, err)
	}
	if len(gSig) != ed25519.SignatureSize {
		return nil, "", nil, fmt.Errorf("%w: global sig size %d", ErrSigFileMalformed, len(gSig))
	}
	return blob, trusted, gSig, nil
}

// VerifyGlobalComment checks that trustedComment was not tampered with
// post-signing. Called by the verifier after it has already validated
// the main payload signature.
func VerifyGlobalComment(pub PublicKey, sigBlob []byte, trustedComment string, globalSig []byte) error {
	if len(sigBlob) != 2+8+ed25519.SignatureSize {
		return fmt.Errorf("%w: sig blob size %d", ErrSignatureMalformed, len(sigBlob))
	}
	if len(globalSig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: global sig size %d", ErrSignatureMalformed, len(globalSig))
	}
	rawSig := sigBlob[10:]
	globalInput := make([]byte, 0, len(rawSig)+len(trustedComment))
	globalInput = append(globalInput, rawSig...)
	globalInput = append(globalInput, []byte(trustedComment)...)
	if !ed25519.Verify(pub.Key, globalInput, globalSig) {
		return ErrSignatureInvalid
	}
	return nil
}
