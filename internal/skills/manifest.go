// Skill manifest + signature handling for #389. The signed envelope
// owns identity, version, kind, and the [[tools.declares]] list. The
// SKILL.md body is treated as the bundle: its bytes are hashed into
// the trusted comment so any tamper between sign and load is rejected
// the same way WASM bundles are (cf. internal/runtime/wasm/loader.go).
//
// Discovery metadata (triggers, tier) stays in SKILL.md YAML
// frontmatter — those drive when a skill surfaces, not what it can do,
// and live outside the security envelope on purpose.
//
// This file does NOT call envelope.Verify directly: the #388 archtest
// pins envelope.Verify's only runtime consumer to
// runtime.Instantiator.InstantiateVerified. The skill loader builds an
// envelope.VerifyInput and hands it to the Instantiator (next step).
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// manifestFile + sigFile are the on-disk names the loader looks for.
// Mirror of the WASM loader convention so contributors don't need to
// remember two layouts.
const (
	manifestFile = "manifest.toml"
	sigFile      = "manifest.sig"
)

// SkillBundle carries the byte sequences a skill needs at instantiate
// time. The VerifyInput is what runtime.Instantiator.InstantiateVerified
// consumes; the BundlePath / ManifestPath fields are for log lines.
//
// The verify pipeline is run against these in-memory bytes only — no
// disk re-read happens between bundle assembly and verification, which
// is the §7.10 TOCTOU guarantee.
type SkillBundle struct {
	VerifyInput  envelope.VerifyInput
	BundlePath   string // absolute path to SKILL.md, for logs
	ManifestPath string // absolute path to manifest.toml, for logs
}

// AutoSigner is the narrow surface the loader needs to mint a daemon
// signature for unsigned user skills (§7.3 Tier 2). A nil AutoSigner
// disables auto-signing entirely — unsigned skills are then rejected
// at load. Production daemons wire a non-nil signer; tests that only
// exercise the verify path pass nil.
type AutoSigner interface {
	Sign(manifestBytes, bundleBytes []byte) ([]byte, error)
}

// loadOptions bundles the inputs prepareSkillBundle needs without
// adding five positional parameters. Production callers populate every
// field; tests pass partial structs.
type loadOptions struct {
	TrustStore envelope.TrustStore
	AutoSign   AutoSigner    // nil → unsigned skills rejected
	Now        func() time.Time
	Logger     func(format string, args ...any)
}

// errSkillManifestNotFound is returned when manifest.toml is absent.
// Callers handle this as a soft skip (legacy YAML-only skill) until
// the migration window closes — see Étape 8.
var errSkillManifestNotFound = errors.New("skills: manifest.toml not found")

// prepareSkillBundle reads manifest.toml + SKILL.md from skillDir and
// assembles the byte sequences runtime.Instantiator.InstantiateVerified
// will consume. When manifest.sig is absent and an AutoSigner is
// configured, the loader mints one with the daemon key and persists it
// (§7.3 Tier 2 — same convention as the WASM loader).
//
// This function does NOT call envelope.Verify — the single sanctioned
// call site lives in runtime.Instantiator.InstantiateVerified (#388
// archtest pin). What it returns goes straight into that pipeline.
//
// Failure modes:
//
//   - manifest.toml missing → errSkillManifestNotFound (caller decides
//     whether to skip the skill or fail the load)
//   - SKILL.md missing → error (a skill without a body is meaningless)
//   - signature missing AND AutoSigner nil → ErrSigFileMalformed
//
// The bundle bytes are SKILL.md only. References (.md sidecars
// flattened by parseSkill) are not part of the bundle — they remain
// unsigned helper material that the prompt body inlines at flatten
// time. If we needed to pin them too, the right move is to canonicalise
// the flatten output and hash that; deferred until we hit a real attack
// vector that requires it.
func prepareSkillBundle(skillDir string, opts loadOptions) (*SkillBundle, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = func(string, ...any) {}
	}

	manifestPath := filepath.Join(skillDir, manifestFile)
	manifestBytes, err := os.ReadFile(manifestPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errSkillManifestNotFound
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", manifestFile, err)
	}

	bundlePath := filepath.Join(skillDir, "SKILL.md")
	bundleBytes, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	sigPath := filepath.Join(skillDir, sigFile)
	sigBytes, err := os.ReadFile(sigPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if opts.AutoSign == nil {
			return nil, fmt.Errorf("%s missing and no auto-signer configured: %w", sigFile, envelope.ErrSigFileMalformed)
		}
		sigBytes, err = opts.AutoSign.Sign(manifestBytes, bundleBytes)
		if err != nil {
			return nil, fmt.Errorf("auto-sign: %w", err)
		}
		if err := os.WriteFile(sigPath, sigBytes, 0o644); err != nil {
			return nil, fmt.Errorf("persist sig: %w", err)
		}
		opts.Logger("[skills] auto-signed %s with daemon key", filepath.Base(skillDir))
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", sigFile, err)
	}

	return &SkillBundle{
		VerifyInput: envelope.VerifyInput{
			ManifestTOML: manifestBytes,
			Signature:    sigBytes,
			Bundle:       bundleBytes,
			TrustStore:   opts.TrustStore,
		},
		BundlePath:   bundlePath,
		ManifestPath: manifestPath,
	}, nil
}

// daemonAutoSigner adapts an envelope private key to the AutoSigner
// surface. The trusted-comment format mirrors the WASM loader (§7.3
// Tier 2 stop-gap with bundle hash in the comment) — keeps verify
// behaviour aligned across capability kinds.
type daemonAutoSigner struct {
	priv envelope.PrivateKey
	now  func() time.Time
}

// NewDaemonAutoSigner builds an AutoSigner backed by the daemon's
// local key (the one minted by wasm.LoadOrGenerateDaemonKey). The now
// closure is injected so tests can pin timestamps.
func NewDaemonAutoSigner(priv envelope.PrivateKey, now func() time.Time) AutoSigner {
	if now == nil {
		now = time.Now
	}
	return &daemonAutoSigner{priv: priv, now: now}
}

func (s *daemonAutoSigner) Sign(manifestBytes, bundleBytes []byte) ([]byte, error) {
	canonical, err := envelope.Canonicalize(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	sig, err := envelope.Sign(s.priv, canonical)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	hash := sha256.Sum256(bundleBytes)
	tc := envelope.TrustedComment{
		BundleID:   "auto-signed-" + hex.EncodeToString(hash[:4]),
		BundleHash: hex.EncodeToString(hash[:]),
		SignedAt:   s.now().UTC(),
	}
	return envelope.EncodeSignatureFile(s.priv, sig, envelope.BuildTrustedComment(tc))
}
