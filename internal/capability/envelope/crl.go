package envelope

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"golang.org/x/text/unicode/norm"
)

// CRL — signed list of revoked publisher keys, distributed by alf
// release infra (§7.7 / §8). Each entry pins a not-valid-after
// timestamp; bundles signed at-or-after that timestamp are rejected
// even if the key is still in the operator's trust store.
//
// Wire format (no sidecar): the SignedCRL JSON wraps a CRL payload
// plus a base64 signature blob over the canonical JSON of that
// payload. ParseSignedCRL re-canonicalizes the payload server-side
// before verifying, so signer and verifier agree on byte layout
// without sidecar gymnastics.
type CRL struct {
	Version    int        `json:"alf_crl_version"`
	IssuedAt   time.Time  `json:"issued_at"`
	NextUpdate time.Time  `json:"next_update"`
	Entries    []CRLEntry `json:"entries"`
}

// CRLEntry — one revoked key. KeyID is 16-char uppercase hex on the
// wire (matches KeyID.Hex()).
type CRLEntry struct {
	KeyID         KeyID     `json:"key_id"`
	NotValidAfter time.Time `json:"not_valid_after"`
	Reason        string    `json:"reason,omitempty"`
}

// SignedCRL is the on-disk / on-wire form. Payload is what's signed.
// Signature is base64 of a minisign sig blob (algo+keyid+ed25519 sig)
// over CanonicalCRLBytes(Payload).
type SignedCRL struct {
	Payload   CRL    `json:"crl"`
	Signature string `json:"signature"`
}

// CRLEnvelopeVersion is the only version this build emits + accepts.
// Bumping requires a parser-version branch; today there is none.
const CRLEnvelopeVersion = 1

var (
	ErrCRLMalformed       = errors.New("envelope: CRL malformed")
	ErrCRLVersion         = errors.New("envelope: CRL version unsupported")
	ErrCRLSignatureFormat = errors.New("envelope: CRL signature blob malformed")
	ErrCRLSignatureInvalid = errors.New("envelope: CRL signature does not verify")
	ErrCRLTimeRange       = errors.New("envelope: CRL time range invalid")
)

// MarshalJSON — KeyID emits as 16-char uppercase hex.
func (k KeyID) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.Hex())
}

// UnmarshalJSON — KeyID parses 16-char uppercase hex.
func (k *KeyID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseKeyIDHex(s)
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// ParseKeyIDHex decodes 16-char uppercase hex into a KeyID. Lowercase
// is also accepted (operators copy-paste from logs).
func ParseKeyIDHex(s string) (KeyID, error) {
	if len(s) != 16 {
		return KeyID{}, fmt.Errorf("%w: keyid hex %q wrong length (got %d, want 16)", ErrCRLMalformed, s, len(s))
	}
	var k KeyID
	for i := 0; i < 8; i++ {
		hi, err1 := hexNibble(s[2*i])
		lo, err2 := hexNibble(s[2*i+1])
		if err1 != nil || err2 != nil {
			return KeyID{}, fmt.Errorf("%w: keyid hex %q non-hex char", ErrCRLMalformed, s)
		}
		k[i] = hi<<4 | lo
	}
	return k, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("non-hex byte %q", c)
}

// CanonicalCRLBytes returns the deterministic JSON of a CRL payload —
// the bytes the signer hashes and the verifier re-derives. Same JCS
// rules as Canonicalize (§7.10): alphabetical keys at every level,
// NFC normalization on strings, RFC 3339 UTC times, no whitespace.
//
// We round-trip through map[string]any so we share normaliseValue
// with the TOML pipeline. CRLs are small (KB-range), so the cost is
// negligible.
func CanonicalCRLBytes(c CRL) ([]byte, error) {
	c.IssuedAt = c.IssuedAt.UTC()
	c.NextUpdate = c.NextUpdate.UTC()
	for i := range c.Entries {
		c.Entries[i].NotValidAfter = c.Entries[i].NotValidAfter.UTC()
	}

	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("envelope: marshal CRL: %w", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, fmt.Errorf("envelope: round-trip CRL: %w", err)
	}
	normalised := normaliseValue(asMap)

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalised); err != nil {
		return nil, fmt.Errorf("envelope: marshal canonical CRL: %w", err)
	}
	b := out.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// EncodeSignedCRL produces the wire form: signs the canonical payload
// with priv, base64-encodes the sig blob, marshals the {crl, signature}
// envelope. Used by alf release tooling, not by the daemon.
func EncodeSignedCRL(c CRL, priv PrivateKey) ([]byte, error) {
	if c.Version == 0 {
		c.Version = CRLEnvelopeVersion
	}
	canonical, err := CanonicalCRLBytes(c)
	if err != nil {
		return nil, err
	}
	sigBlob, err := Sign(priv, canonical)
	if err != nil {
		return nil, err
	}
	signed := SignedCRL{
		Payload:   c,
		Signature: base64.StdEncoding.EncodeToString(sigBlob),
	}
	return json.Marshal(signed)
}

// ParseSignedCRL verifies the embedded signature against releasePub and
// returns the typed CRL. Sequence:
//
//  1. Unmarshal the {crl, signature} envelope
//  2. Decode the base64 sig blob (algo + keyid + ed25519 sig)
//  3. Re-canonicalize the parsed payload — DO NOT trust raw input bytes
//  4. Verify sig against canonical bytes (algo + keyid + Ed25519 → BLAKE2b-512)
//  5. Validate version + time bounds
//
// Step 3 is load-bearing: a CRL JSON with cosmetic differences (key
// order, whitespace) verifies iff its semantic content matches what
// was signed. Step 4 reuses VerifySignature; the sig blob's keyid
// must match releasePub.ID (built into VerifySignature).
func ParseSignedCRL(raw []byte, releasePub PublicKey) (*CRL, error) {
	var signed SignedCRL
	if err := json.Unmarshal(raw, &signed); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCRLMalformed, err)
	}
	if signed.Signature == "" {
		return nil, fmt.Errorf("%w: missing signature field", ErrCRLMalformed)
	}
	sigBlob, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %w", ErrCRLSignatureFormat, err)
	}
	canonical, err := CanonicalCRLBytes(signed.Payload)
	if err != nil {
		return nil, err
	}
	if err := VerifySignature(releasePub, canonical, sigBlob); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCRLSignatureInvalid, err)
	}
	if err := validateCRLPayload(&signed.Payload); err != nil {
		return nil, err
	}
	return &signed.Payload, nil
}

// validateCRLPayload runs post-signature semantic checks. A signed CRL
// with semantically-invalid content (next_update before issued_at,
// version bump beyond what we support) is rejected — the signature
// proves intent but not correctness.
func validateCRLPayload(c *CRL) error {
	if c.Version != CRLEnvelopeVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrCRLVersion, c.Version, CRLEnvelopeVersion)
	}
	if c.IssuedAt.IsZero() {
		return fmt.Errorf("%w: missing issued_at", ErrCRLMalformed)
	}
	if !c.NextUpdate.IsZero() && c.NextUpdate.Before(c.IssuedAt) {
		return fmt.Errorf("%w: next_update %s before issued_at %s",
			ErrCRLTimeRange, c.NextUpdate.Format(time.RFC3339), c.IssuedAt.Format(time.RFC3339))
	}
	for i, e := range c.Entries {
		if e.NotValidAfter.IsZero() {
			return fmt.Errorf("%w: entry %d (%s) missing not_valid_after",
				ErrCRLMalformed, i, e.KeyID.Hex())
		}
	}
	// Sort entries by KeyID hex so callers get deterministic order.
	sort.SliceStable(c.Entries, func(i, j int) bool {
		return c.Entries[i].KeyID.Hex() < c.Entries[j].KeyID.Hex()
	})
	c.IssuedAt = c.IssuedAt.UTC()
	c.NextUpdate = c.NextUpdate.UTC()
	for i := range c.Entries {
		c.Entries[i].NotValidAfter = c.Entries[i].NotValidAfter.UTC()
		c.Entries[i].Reason = norm.NFC.String(c.Entries[i].Reason)
	}
	return nil
}
