package envelope

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

// VerifyInput carries every byte sequence the verify pipeline consumes.
// TOCTOU defence (§7.10 + #388 deliverable 3): inputs are []byte, never
// paths. Callers read each file ONCE; the same in-memory slice is passed
// to Verify and, on success, to the loader (wazero.Instantiate or skill
// parser). A disk-level modification between "verify" and "load" is
// physically ignored because no re-read happens.
type VerifyInput struct {
	// ManifestTOML is the raw bytes of the authored manifest.toml.
	ManifestTOML []byte

	// Signature is the raw bytes of the detached minisign signature
	// file (manifest.sig). ParseSignatureFile will break it into its
	// three pieces — main signature blob, trusted comment, global sig.
	Signature []byte

	// Bundle is the primary artefact (.wasm for wasm-tool/wasm-app,
	// bundle.zip for marketplace-app). Optional today: kinds without
	// an executable artefact (skill, reserved) may pass nil.
	//
	// When provided, BundleSHA256 is cross-checked against the hash
	// embedded in the signature's trusted comment (see §7.10.3 — full
	// envelope record is deferred; current trusted comment carries the
	// bundle hash hex for tamper detection).
	Bundle []byte

	// TrustStore is the authority on which keys are accepted. A miss
	// on Lookup returns ErrSignerNotTrusted regardless of signature
	// correctness.
	TrustStore TrustStore
}

// VerifiedManifest is the success value of the verify pipeline. It
// carries the typed Manifest (already validated), the canonical bytes
// that were actually signed (useful for audit logging), and the
// signer's KeyID (useful for access control decisions downstream).
type VerifiedManifest struct {
	Manifest       *Manifest
	CanonicalBytes []byte
	SignerID       KeyID
	TrustedComment string

	// SignedAt is the RFC 3339 timestamp the signer embedded in the
	// trusted comment (tamper-protected by the minisign global
	// signature). Required — a signature without signed_at cannot
	// support CRL time-bound revocation (§7.7), so the verify pipeline
	// rejects it with ErrTrustedCommentMalformed.
	SignedAt time.Time
}

// Verify is the single load-time entry point for #388 (the archtest
// that gates this will live in the runtime package once Instantiator
// consumes it in step 6). Implements the §7.10 pipeline:
//
//  1. Parse the signature file into its three components
//  2. Look up the signer's public key in the trust store (fail-closed
//     with ErrSignerNotTrusted on miss — before any cryptographic work)
//  3. Validate the manifest (schema, unknown fields, deferred blocks)
//  4. Canonicalize the manifest bytes
//  5. Verify the main signature against the canonical bytes
//  6. Verify the global signature against the trusted comment
//  7. (If a Bundle is present) cross-check its SHA-256 against the
//     "bundle_sha256=<hex>" field embedded in the trusted comment
//
// Full envelope-record JSON structure per §7.10.3 (separate signed
// record that carries bundle_hash, algorithm, signed_at, etc.) is
// deferred to a polish pass; the current trusted-comment carries the
// bundle hash as a stop-gap. The gap is tracked in comment on #388.
// All other properties from §7.10 (canonicalisation, algo dispatch,
// scheme-substitution rejection) are active.
func Verify(in VerifyInput) (*VerifiedManifest, error) {
	if in.TrustStore == nil {
		return nil, fmt.Errorf("envelope: verify requires a TrustStore")
	}

	// 1. Parse sig file.
	sigBlob, trustedComment, globalSig, err := ParseSignatureFile(in.Signature)
	if err != nil {
		return nil, err
	}

	// 2. Trust-store lookup (signer ID is in bytes 2..10 of the sig blob).
	var signerID KeyID
	copy(signerID[:], sigBlob[2:10])
	pub, ok, lookupErr := in.TrustStore.Lookup(signerID)
	if lookupErr != nil {
		return nil, fmt.Errorf("envelope: trust store lookup: %w", lookupErr)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSignerNotTrusted, signerID.Hex())
	}

	// 3. Schema validation of the manifest.
	manifest, err := Validate(in.ManifestTOML)
	if err != nil {
		return nil, err
	}

	// 4. Canonicalize.
	canonical, err := Canonicalize(in.ManifestTOML)
	if err != nil {
		return nil, fmt.Errorf("envelope: canonicalize: %w", err)
	}

	// 5. Main signature verification against canonical bytes.
	if err := VerifySignature(pub, canonical, sigBlob); err != nil {
		return nil, err
	}

	// 6. Global signature verification (trusted comment integrity).
	if err := VerifyGlobalComment(pub, sigBlob, trustedComment, globalSig); err != nil {
		return nil, fmt.Errorf("envelope: trusted comment integrity: %w", err)
	}

	// 7. Parse the structured trusted comment — required fields only
	// (signed_at). BundleHash is cross-checked against the actual
	// bundle bytes when provided.
	tc, err := ParseTrustedComment(trustedComment)
	if err != nil {
		return nil, err
	}

	if in.Bundle != nil {
		if tc.BundleHash == "" {
			return nil, fmt.Errorf("%w: trusted comment lacks bundle_sha256= field", ErrBundleHashMissing)
		}
		actual := sha256hex(in.Bundle)
		if tc.BundleHash != actual {
			return nil, fmt.Errorf("%w: expected=%s actual=%s", ErrBundleHashMismatch, tc.BundleHash, actual)
		}
	}

	return &VerifiedManifest{
		Manifest:       manifest,
		CanonicalBytes: canonical,
		SignerID:       pub.ID,
		TrustedComment: trustedComment,
		SignedAt:       tc.SignedAt,
	}, nil
}

// Typed errors specific to the high-level verify pipeline.
var (
	ErrSignerNotTrusted   = errors.New("envelope: signer key not in trust store")
	ErrBundleHashMissing  = errors.New("envelope: bundle hash not declared in trusted comment")
	ErrBundleHashMismatch = errors.New("envelope: bundle bytes do not match declared SHA-256")
)

// sha256hex returns the lowercase hex SHA-256 of data.
func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range h {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

// (extractBundleHash removed — its responsibility moved into the
// typed ParseTrustedComment in comment.go. That parser also extracts
// signed_at, which was the missing field on the road to #396 CRL.)
