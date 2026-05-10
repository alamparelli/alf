package marketplace

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// ErrBundleManifestMissing signals that the downloaded bundle ZIP
// has no `manifest.toml` at its root — required for envelope.Verify
// per #384's "bundles ship with a canonical envelope" deliverable.
var ErrBundleManifestMissing = errors.New("marketplace: bundle has no manifest.toml at root")

// ErrBundleSignatureMissing signals that the marketplace registry
// returned the bundle ZIP but no detached `bundle.sig`. Distinct
// from a transport error so the caller can surface a clear
// "registry has not yet been upgraded for v0.8.0 signed bundles"
// message rather than a confusing 404.
var ErrBundleSignatureMissing = errors.New("marketplace: registry did not serve bundle.sig (server upgrade required for v0.8.0)")

// ErrBundleManifestNotMarketplace pins that a manifest.toml inside
// the downloaded bundle declares a kind other than "marketplace-app"
// or "wasm-app". The deprecated marketplace-app form and its
// successor wasm-app share an identical envelope shape; both are
// accepted (per MANIFEST-SCHEMA §4.6). Anything else is rejected
// at install time.
var ErrBundleManifestNotMarketplace = errors.New("marketplace: bundle manifest kind must be wasm-app (marketplace-app retired per §3.3)")

// MaxBundleManifestSize bounds the in-memory read of manifest.toml
// from inside the bundle ZIP. The schema's actual ceiling is much
// smaller (typical manifest is well under 4 KiB); 64 KiB leaves
// plenty of headroom for futures while refusing to spool a hostile
// "manifest" that secretly carries 200 MiB of payload.
const MaxBundleManifestSize = 64 << 10

// MaxBundleSignatureSize bounds the bundle.sig download. A minisign
// signature is on the order of 200 bytes; 4 KiB tolerates trusted
// comment growth without giving an attacker a memory bomb.
const MaxBundleSignatureSize = 4 << 10

// MaxBundleManifestJSONSize bounds the in-memory read of
// manifest.json — the legacy view the activate path consumes for
// app metadata (permissions, services, tools). Smaller than the
// envelope ceiling on the assumption that a real-world manifest
// fits comfortably in 16 KiB; this is a memory-bomb defence, not a
// design constraint.
const MaxBundleManifestJSONSize = 16 << 10

// readManifestJSONFromZip extracts the legacy `manifest.json` from
// the in-memory bundle ZIP, parses it into a Manifest, and returns
// the result. Used by #402 to diff a bundle's declared permissions
// against the cached post-install state BEFORE the update touches
// the on-disk app dir.
//
// Failure modes mapped to typed errors:
//   - manifest.json absent: ErrBundleManifestJSONMissing
//   - exceeds size cap:    wrapped fmt error
//   - JSON parse error:    wrapped fmt error
//
// Bundles that pre-date the v0.8.0 envelope migration may carry
// only manifest.json (no manifest.toml); those fail at verifyBundle
// long before this helper runs. By the time #402's diff path
// invokes this, the envelope check has already passed.
func readManifestJSONFromZip(zipBytes []byte) (*Manifest, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("marketplace: zip reader: %w", err)
	}
	for _, zf := range zr.File {
		if zf.Name != "manifest.json" {
			continue
		}
		if zf.UncompressedSize64 > MaxBundleManifestJSONSize {
			return nil, fmt.Errorf("marketplace: manifest.json exceeds %d-byte ceiling: got %d", MaxBundleManifestJSONSize, zf.UncompressedSize64)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("marketplace: open manifest.json: %w", err)
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, MaxBundleManifestJSONSize))
		if err != nil {
			return nil, fmt.Errorf("marketplace: read manifest.json: %w", err)
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("marketplace: parse manifest.json: %w", err)
		}
		// SEC-001: never trust the bundle-declared trust flag.
		m.Trusted = false
		return &m, nil
	}
	return nil, ErrBundleManifestJSONMissing
}

// ErrBundleManifestJSONMissing signals that the verified bundle has
// no `manifest.json` at its root. The activate path consumes this
// file for permissions/services/tools metadata; without it the app
// cannot be installed even if the envelope (manifest.toml) is
// well-formed and signed.
var ErrBundleManifestJSONMissing = errors.New("marketplace: bundle has no manifest.json at root")

// readManifestFromZip extracts `manifest.toml` from inside the
// bundle ZIP without writing anything to disk. The returned bytes
// are the raw TOML — caller passes them to envelope.Verify
// alongside the detached bundle.sig.
//
// Failure modes mapped to typed errors so callers can distinguish:
//   - ZIP unreadable (corrupt download): wrapped fmt error
//   - manifest.toml absent: ErrBundleManifestMissing
//   - manifest.toml exceeds MaxBundleManifestSize: wrapped fmt error
func readManifestFromZip(zipBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("marketplace: zip reader: %w", err)
	}
	for _, zf := range zr.File {
		if zf.Name != "manifest.toml" {
			continue
		}
		if zf.UncompressedSize64 > MaxBundleManifestSize {
			return nil, fmt.Errorf("marketplace: manifest.toml exceeds %d-byte ceiling: got %d", MaxBundleManifestSize, zf.UncompressedSize64)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("marketplace: open manifest.toml: %w", err)
		}
		defer rc.Close()
		buf, err := io.ReadAll(io.LimitReader(rc, MaxBundleManifestSize))
		if err != nil {
			return nil, fmt.Errorf("marketplace: read manifest.toml: %w", err)
		}
		return buf, nil
	}
	return nil, ErrBundleManifestMissing
}

// verifyBundle wires the marketplace install path through the
// shared envelope.Verify pipeline. Inputs:
//
//   - bundleBytes: the downloaded bundle.zip in memory.
//   - sigBytes: the detached bundle.sig — already constrained to
//     MaxBundleSignatureSize by the HTTP layer.
//   - store: the trust store the daemon shares with the WASM and
//     skills loaders. The marketplace pubkey is one entry in it
//     (auto-added at boot when the binary embed is non-empty);
//     third-party signers land via `alf trust add`.
//
// Returns the parsed and verified manifest. On any verification
// failure, returns the underlying envelope error wrapped — callers
// surface those directly so operators see exactly which guard
// rejected the bundle (untrusted signer, signature mismatch,
// canonicalisation failure, key revoked, etc).
//
// Acceptance criteria covered (#384):
//   - Unsigned bundle cannot be installed: caller's HTTP fetch of
//     bundle.sig 404s → ErrBundleSignatureMissing surfaced before
//     this function is even called.
//   - Bundle signed with unknown key: envelope.ErrSignerNotTrusted.
//   - Tampered bundle (single byte flip): envelope.ErrSignatureInvalid
//     (manifest mismatch) or the bundle-hash check inside Verify.
//   - Manifest declares kind != wasm-app:
//     ErrBundleManifestNotMarketplace.
//
// Doctrine: ARCHITECTURE-SECURITY.md §4.1 + MANIFEST-SCHEMA.md §3.3.
// The legacy "marketplace-app" kind is retired — marketplace bundles
// are wasm-app only. Manifests declaring marketplace-app are rejected
// here even when otherwise validly signed; the parser still accepts
// the value for legacy fixture compatibility, but the install path
// refuses it (#420).
func verifyBundle(bundleBytes, sigBytes []byte, store envelope.TrustStore) (*envelope.Manifest, error) {
	manifestTOML, err := readManifestFromZip(bundleBytes)
	if err != nil {
		return nil, err
	}
	in := envelope.VerifyInput{
		ManifestTOML: manifestTOML,
		Signature:    sigBytes,
		Bundle:       bundleBytes,
		TrustStore:   store,
	}
	res, err := envelope.Verify(in)
	if err != nil {
		return nil, err
	}
	if res.Manifest.Kind != envelope.KindWASMApp {
		return nil, fmt.Errorf("%w: got %q", ErrBundleManifestNotMarketplace, res.Manifest.Kind)
	}
	return res.Manifest, nil
}
