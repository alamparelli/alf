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
	"sort"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/runtime/events"
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
	Runtime    *Runtime
	Registry   CapabilityRegistry
	DaemonPriv envelope.PrivateKey
	TrustStore envelope.TrustStore
	Logger     func(format string, args ...any)
	Now        func() time.Time // injected for deterministic tests

	// CrossFlow is the publisher-topic registry the loader populates
	// in pass 1 from events.exports of every signed manifest. The
	// instantiator queries it in pass 2 to decide whether to forge an
	// EventSub handle. When nil, no events handles are forged
	// regardless of manifest declarations — used by tests that don't
	// exercise the events path.
	CrossFlow *events.MemoryRegistry

	// SnapshotDir is where active-flows.json is written after every
	// load. When empty, snapshots are skipped (also tests).
	SnapshotDir string
}

// preLoadedBundle holds the artefacts pass 1 collected for one bundle.
// Pass 2 instantiates from these without re-reading disk.
type preLoadedBundle struct {
	dir          string
	displayName  string
	manifestPath string
	manifest     *envelope.Manifest
	manifestRaw  []byte
	wasmBytes    []byte
	sigBytes     []byte
}

// LoadDir scans root, processes each <root>/<id>/ directory, and
// attempts to register the discovered bundles in the capability
// registry. Two-pass per §3.3 + #399:
//
//	Pass 1: read + validate every manifest, auto-sign any that lack
//	  manifest.sig, populate the cross-flow registry from events.exports
//	  declarations.
//	Pass 2: forge handles + instantiate. EventSub handles are only
//	  forged when the cross-flow registry confirms the cited publisher
//	  is installed AND exports the topic.
//
// Two-pass is necessary so a subscriber loaded before its publisher in
// alphabetical order still gets its handle. A per-bundle failure in
// either pass is logged and accumulated; it never aborts the whole
// boot sequence — one bad bundle must not prevent others from loading.
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

	// Sort entries so iteration order is deterministic for tests + logs.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var (
		pres []preLoadedBundle
		errs []error
	)

	// Pass 1: collect + validate + auto-sign + register exports.
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bundleDir := filepath.Join(root, e.Name())
		pre, err := l.preLoad(bundleDir, e.Name())
		if err != nil {
			l.Logger("[wasm-loader] %s: %v", e.Name(), err)
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		// Register every export declared in this manifest. Empty
		// events.exports is a no-op; the registry stays empty.
		if l.CrossFlow != nil {
			for _, ex := range pre.manifest.Events.Exports {
				l.CrossFlow.RegisterExport(capability.ID(pre.manifest.ID), ex.Topic)
			}
		}
		pres = append(pres, pre)
	}

	// Pass 2: forge + instantiate. The instantiator (when wired with
	// bus + cross-flow registry) sees the populated registry and forges
	// EventSub handles only for resolved cross-flows.
	var (
		loaded []string
		flows  []events.FlowEntry
	)
	for _, pre := range pres {
		id, err := l.instantiateBundle(ctx, pre)
		if err != nil {
			l.Logger("[wasm-loader] %s: %v", pre.displayName, err)
			errs = append(errs, fmt.Errorf("%s: %w", pre.displayName, err))
			continue
		}
		loaded = append(loaded, id)
		l.Logger("[wasm-loader] registered %s", id)

		// Surface cross-flows for §3.3 hard rule #5 ("surfaced at install").
		// Boot-time observability only — interactive ratification arrives
		// with #395 reading the snapshot JSON.
		for _, sub := range pre.manifest.Events.Subscribes {
			if l.CrossFlow == nil || !l.CrossFlow.HasExport(capability.ID(sub.From), sub.Topic) {
				l.Logger("[events] subscribe ignored (publisher not installed or topic not exported): %s wants %s:%q", id, sub.From, sub.Topic)
				continue
			}
			l.Logger("[events] cross-flow established: %s ← %s:%q", id, sub.From, sub.Topic)
			flows = append(flows, events.FlowEntry{
				Publisher:  sub.From,
				Topic:      sub.Topic,
				Subscriber: id,
			})
		}
	}

	// Snapshot for the Option B UX. Always write — empty array still
	// reflects "loader ran, zero flows" which is meaningful.
	if l.SnapshotDir != "" {
		if err := events.WriteSnapshot(l.SnapshotDir, flows, l.Now); err != nil {
			l.Logger("[events] snapshot write failed: %v", err)
		} else {
			l.Logger("[events] active flows snapshot written: %s/events/%s (%d flows)", l.SnapshotDir, events.SnapshotFile, len(flows))
		}
	}

	return loaded, errs
}

// preLoad runs pass 1 for one bundle: read manifest + wasm bytes,
// validate the manifest, auto-sign if no sig is present. Returns the
// fully-prepared preLoadedBundle ready for pass 2 instantiation.
func (l *Loader) preLoad(bundleDir, displayName string) (preLoadedBundle, error) {
	manifestPath := filepath.Join(bundleDir, "manifest.toml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return preLoadedBundle{}, fmt.Errorf("read manifest.toml: %w", err)
	}

	manifest, err := envelope.Validate(manifestBytes)
	if err != nil {
		return preLoadedBundle{}, fmt.Errorf("validate manifest: %w", err)
	}
	wasmPath := filepath.Join(bundleDir, manifest.ID+".wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return preLoadedBundle{}, fmt.Errorf("read %s.wasm: %w", manifest.ID, err)
	}

	// Auto-sign if no manifest.sig is present. Once signed, the
	// signature is persisted so subsequent boots verify against
	// the same key without re-signing.
	sigPath := filepath.Join(bundleDir, "manifest.sig")
	sigBytes, err := os.ReadFile(sigPath)
	if errors.Is(err, fs.ErrNotExist) {
		sigBytes, err = l.autoSign(manifestBytes, wasmBytes)
		if err != nil {
			return preLoadedBundle{}, fmt.Errorf("auto-sign: %w", err)
		}
		if err := os.WriteFile(sigPath, sigBytes, 0o644); err != nil {
			return preLoadedBundle{}, fmt.Errorf("persist sig: %w", err)
		}
		l.Logger("[wasm-loader] auto-signed %s with daemon key %s", manifest.ID, l.DaemonPriv.ID.Hex())
	} else if err != nil {
		return preLoadedBundle{}, fmt.Errorf("read sig: %w", err)
	}

	return preLoadedBundle{
		dir:          bundleDir,
		displayName:  displayName,
		manifestPath: manifestPath,
		manifest:     manifest,
		manifestRaw:  manifestBytes,
		wasmBytes:    wasmBytes,
		sigBytes:     sigBytes,
	}, nil
}

// instantiateBundle runs pass 2 for one pre-loaded bundle: verify +
// forge + instantiate via Runtime, register the adapter.
func (l *Loader) instantiateBundle(ctx context.Context, pre preLoadedBundle) (string, error) {
	in := envelope.VerifyInput{
		ManifestTOML: pre.manifestRaw,
		Signature:    pre.sigBytes,
		Bundle:       pre.wasmBytes,
		TrustStore:   l.TrustStore,
	}
	mod, err := l.Runtime.Instantiate(ctx, in, pre.wasmBytes, pre.dir)
	if err != nil {
		return "", fmt.Errorf("instantiate: %w", err)
	}

	adapter := NewAdapter(mod)
	if err := l.Registry.Register(adapter); err != nil {
		_ = adapter.Close(ctx)
		return "", fmt.Errorf("register: %w", err)
	}
	return pre.manifest.ID, nil
}

// autoSign signs the canonicalised manifest with the daemon key and
// embeds the bundle SHA-256 in the trusted comment, mirroring the
// stop-gap format shipped in #388 (`commit 34010c6`). The signed_at
// timestamp comes from the injected clock so tests are deterministic.
//
// SEC-004: refuses to sign manifests that exceed the §7.3 Tier-2
// ceiling. The local daemon key cannot pre-approve cross-flow
// subscriptions or future widening blocks — the operator must
// re-sign with the user-endorsed key (Tier 3) via `alf keygen`
// + `alf sign --key user-endorsed`.
func (l *Loader) autoSign(manifestBytes, wasmBytes []byte) ([]byte, error) {
	manifest, err := envelope.Validate(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	if err := envelope.EnforceTier2Ceiling(manifest); err != nil {
		return nil, err
	}
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
