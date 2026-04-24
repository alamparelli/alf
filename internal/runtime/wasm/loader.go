package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

// CapabilityRegistry is the minimal surface the loader needs from
// capability.Registry. Kept as an interface so tests can supply a
// no-op recorder.
type CapabilityRegistry interface {
	Register(capability.Capability) error
}

// Loader walks a skills.d/wasm/ tree and registers each bundle it
// can verify through the wasm.Runtime + wasm.Adapter stack.
//
// Bundle layout:
//
//	<root>/<id>/manifest.toml   (required)
//	<root>/<id>/<id>.wasm       (required)
//	<root>/<id>/manifest.sig    (optional; auto-signed if missing)
//
// When a bundle lacks manifest.sig, the loader auto-signs with the
// daemon's local key (§7.3 Tier 2). The trust store must already
// contain that daemon public key — the caller sets this up at
// daemon init. This is the interim path for LLM-built bundles
// from step 8; marketplace bundles (step 12 / #384) ship pre-signed.
type Loader struct {
	Runtime      *Runtime
	Registry     CapabilityRegistry
	DaemonPriv   envelope.PrivateKey
	TrustStore   envelope.TrustStore
	Logger       func(format string, args ...any)
	Now          func() time.Time // injected for deterministic tests
}

// LoadDir scans root, processes each <root>/<id>/ directory, and
// attempts to register the discovered bundles in the capability
// registry. A per-bundle failure is logged and accumulated; it
// never aborts the whole boot sequence — one bad bundle must not
// prevent others from loading.
//
// Returns the list of successfully-registered capability IDs and
// the slice of per-bundle errors (empty on full success).
func (l *Loader) LoadDir(ctx context.Context, root string) ([]string, []error) {
	if l == nil || l.Runtime == nil || l.Registry == nil {
		return nil, []error{fmt.Errorf("wasm: loader not initialised")}
	}
	if l.Logger == nil {
		l.Logger = func(format string, args ...any) {}
	}
	if l.Now == nil {
		l.Now = time.Now
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No skills.d/wasm yet — normal on fresh install.
			return nil, nil
		}
		return nil, []error{fmt.Errorf("wasm: readdir %s: %w", root, err)}
	}

	var (
		loaded []string
		errs   []error
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bundleDir := filepath.Join(root, e.Name())
		id, err := l.loadBundle(ctx, bundleDir)
		if err != nil {
			l.Logger("[wasm-loader] %s: %v", e.Name(), err)
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		loaded = append(loaded, id)
		l.Logger("[wasm-loader] registered %s", id)
	}
	return loaded, errs
}

func (l *Loader) loadBundle(ctx context.Context, bundleDir string) (string, error) {
	manifestPath := filepath.Join(bundleDir, "manifest.toml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read manifest.toml: %w", err)
	}

	// Peek at the manifest to discover the bundle id — we need it
	// to name the .wasm file. Validation re-runs inside
	// envelope.Verify so this peek is throwaway.
	peek, err := envelope.Validate(manifestBytes)
	if err != nil {
		return "", fmt.Errorf("validate manifest: %w", err)
	}
	wasmPath := filepath.Join(bundleDir, peek.ID+".wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return "", fmt.Errorf("read %s.wasm: %w", peek.ID, err)
	}

	// Auto-sign if no manifest.sig is present. Once signed, the
	// signature is persisted so subsequent boots verify against
	// the same key without re-signing.
	sigPath := filepath.Join(bundleDir, "manifest.sig")
	sigBytes, err := os.ReadFile(sigPath)
	if errors.Is(err, fs.ErrNotExist) {
		sigBytes, err = l.autoSign(manifestBytes, wasmBytes)
		if err != nil {
			return "", fmt.Errorf("auto-sign: %w", err)
		}
		if err := os.WriteFile(sigPath, sigBytes, 0o644); err != nil {
			return "", fmt.Errorf("persist sig: %w", err)
		}
		l.Logger("[wasm-loader] auto-signed %s with daemon key %s", peek.ID, l.DaemonPriv.ID.Hex())
	} else if err != nil {
		return "", fmt.Errorf("read sig: %w", err)
	}

	// Run the full verify + forge + instantiate pipeline.
	in := envelope.VerifyInput{
		ManifestTOML: manifestBytes,
		Signature:    sigBytes,
		Bundle:       wasmBytes,
		TrustStore:   l.TrustStore,
	}
	mod, err := l.Runtime.Instantiate(ctx, in, wasmBytes, bundleDir)
	if err != nil {
		return "", fmt.Errorf("instantiate: %w", err)
	}

	adapter := NewAdapter(mod)
	if err := l.Registry.Register(adapter); err != nil {
		_ = adapter.Close(ctx)
		return "", fmt.Errorf("register: %w", err)
	}
	return peek.ID, nil
}

// autoSign signs the canonicalised manifest with the daemon key and
// embeds the bundle SHA-256 in the trusted comment, mirroring the
// stop-gap format shipped in #388 (`commit 34010c6`). The signed_at
// timestamp comes from the injected clock so tests are deterministic.
func (l *Loader) autoSign(manifestBytes, wasmBytes []byte) ([]byte, error) {
	canonical, err := envelope.Canonicalize(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	sig, err := envelope.Sign(l.DaemonPriv, canonical)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	hash := sha256.Sum256(wasmBytes)
	tc := envelope.TrustedComment{
		BundleID:   "auto-signed-" + hex.EncodeToString(hash[:4]),
		BundleHash: hex.EncodeToString(hash[:]),
		SignedAt:   l.Now().UTC(),
	}
	return envelope.EncodeSignatureFile(l.DaemonPriv, sig, envelope.BuildTrustedComment(tc))
}
