package marketplace

import (
	"archive/zip"
	"bytes"
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
var ErrBundleManifestNotMarketplace = errors.New("marketplace: bundle manifest kind must be marketplace-app or wasm-app")

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
//   - Manifest declares kind != marketplace-app|wasm-app:
//     ErrBundleManifestNotMarketplace.
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
	if res.Manifest.Kind != envelope.KindMarketplaceApp && res.Manifest.Kind != envelope.KindWASMApp {
		return nil, fmt.Errorf("%w: got %q", ErrBundleManifestNotMarketplace, res.Manifest.Kind)
	}
	return res.Manifest, nil
}
